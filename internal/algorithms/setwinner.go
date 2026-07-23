package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// SetWinnerConfig controls the per-set Markov set-winner prediction strategy.
//
// Research basis:
//   - Set 1: i.i.d. holds (Klaassen-Magnus 2001, Depken et al. 2021)
//   - Set 2: psychological reversal — set 1 winner underperforms vs i.i.d.
//     by ~2-3% (Depken et al. 2021, Meier et al. 2022)
//   - Set 3 (deciding, best-of-3): set 2 winner has momentum advantage
//     (IZA DP 9315)
//
// The strategy computes Markov match-win probability from the current score,
// applies a per-set psychological adjustment, and buys the side whose
// adjusted fair value exceeds market price by MinEdgeCents.
type SetWinnerConfig struct {
	// PServe: serve point win probability (0.64 ATP, 0.62 WTA).
	PServe float64
	// ReversalPenalty: match-win prob reduction for previous set winner in set 2+.
	// Models psychological reversal (Depken et al. 2021).
	ReversalPenalty float64
	// DecidingSetBoost: match-win prob boost for set 2 winner in set 3 (deciding).
	// Models momentum in deciding sets (IZA DP 9315).
	DecidingSetBoost float64
	// MinEdgeCents: minimum edge (fair_value - price) * 100 to fire.
	MinEdgeCents int
	// MinMarketPrice: don't buy below this price.
	MinMarketPrice float64
	// MaxMarketPrice: don't buy above this price.
	MaxMarketPrice float64
	// CooldownPoints: minimum points between fires per match.
	CooldownPoints int
	// Label: strategy label for orders.
	Label string
}

func DefaultSetWinnerConfig() SetWinnerConfig {
	return SetWinnerConfig{
		PServe:           0.64,
		ReversalPenalty:  0.03,
		DecidingSetBoost: 0.02,
		MinEdgeCents:     3,
		MinMarketPrice:   0.05,
		MaxMarketPrice:   0.92,
		CooldownPoints:   3,
		Label:            "setwinner",
	}
}

// swMatchState tracks set winners for per-set adjustment.
type swMatchState struct {
	setsHome        int
	setsAway        int
	lastSetNum      int
	lastHomeGames   int
	lastAwayGames   int
	lastScorer      int
	set2WinnerHome  bool // set when set 2 completes (for set 3 deciding adjustment)
	set2WinnerKnown bool
	pointsSinceFire int
}

// SetWinnerStrategy uses Markov match-win probability with per-set
// psychological adjustments to find mispriced match-winner markets.
//
// Fires on every point (not just break points), computes adjusted fair
// value for both sides, buys whichever has edge > MinEdgeCents.
// Cooldown prevents over-firing within a match.
type SetWinnerStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64   // market_ticker -> latest YES price
	priceTimes map[string]time.Time // market_ticker -> last price update
	markets    map[string][]string  // event_ticker -> [home, away]
	states     map[string]*swMatchState
	seenPoints map[string]map[string]bool // dedup
	emitter    OrderEmitter
	model      *MarkovModel
	cfg        SetWinnerConfig
	log        *slog.Logger
	replayNow  *time.Time
}

func NewSetWinnerStrategy(emitter OrderEmitter, log *slog.Logger, cfg SetWinnerConfig) *SetWinnerStrategy {
	return &SetWinnerStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		states:     make(map[string]*swMatchState),
		seenPoints: make(map[string]map[string]bool),
		emitter:    emitter,
		model:      NewMarkovModelWithProb(cfg.PServe),
		cfg:        cfg,
		log:        log,
	}
}

// SetSharedMarkovModel replaces the per-strategy model with a shared one.
// Memoization then works across strategies with identical pServe.
func (s *SetWinnerStrategy) SetSharedMarkovModel(m *MarkovModel) {
	s.model = m
}

func (s *SetWinnerStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	if _, ok := s.states[eventTicker]; !ok {
		s.states[eventTicker] = &swMatchState{}
	}
	s.mu.Unlock()
}

func (s *SetWinnerStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.states, eventTicker)
	delete(s.seenPoints, eventTicker)
	s.mu.Unlock()
}

func (s *SetWinnerStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *SetWinnerStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *SetWinnerStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SetWinnerStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SetWinnerStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *SetWinnerStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)
	s.processPoint(eventTicker, p)
}

func (s *SetWinnerStrategy) updateMatchState(eventTicker string, p store.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.states[eventTicker]
	if ms == nil {
		ms = &swMatchState{}
		s.states[eventTicker] = ms
	}

	// Detect set completion: set number incremented since last point.
	// Previous set's final game count tells us who won it.
	if p.SetNumber > ms.lastSetNum && ms.lastSetNum > 0 {
		setWinnerHome := false
		if ms.lastHomeGames > ms.lastAwayGames {
			setWinnerHome = true
			ms.setsHome++
		} else if ms.lastAwayGames > ms.lastHomeGames {
			setWinnerHome = false
			ms.setsAway++
		} else if ms.lastScorer != 0 {
			if ms.lastScorer == 1 {
				setWinnerHome = true
				ms.setsHome++
			} else {
				setWinnerHome = false
				ms.setsAway++
			}
		}
		// Record set 2 winner for deciding-set adjustment
		if ms.lastSetNum == 2 {
			ms.set2WinnerHome = setWinnerHome
			ms.set2WinnerKnown = true
		}
	}

	ms.lastSetNum = p.SetNumber
	ms.lastHomeGames = p.HomeGames
	ms.lastAwayGames = p.AwayGames
	ms.lastScorer = p.Scorer
	ms.pointsSinceFire++
}

func (s *SetWinnerStrategy) processPoint(eventTicker string, p store.Point) {
	// Dedup: same point may arrive twice in replay
	pointKey := fmt.Sprintf("%d:%d:%d", p.SetNumber, p.GameNumber, p.PointNumber)
	s.mu.Lock()
	if s.seenPoints[eventTicker] == nil {
		s.seenPoints[eventTicker] = make(map[string]bool)
	}
	if s.seenPoints[eventTicker][pointKey] {
		s.mu.Unlock()
		return
	}
	s.seenPoints[eventTicker][pointKey] = true
	s.mu.Unlock()

	s.mu.RLock()
	ms := s.states[eventTicker]
	mkts, ok := s.markets[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mkts) < 2 || ms == nil {
		return
	}

	// Cooldown check
	s.mu.RLock()
	cooldownOK := ms.pointsSinceFire >= s.cfg.CooldownPoints
	s.mu.RUnlock()
	if !cooldownOK {
		return
	}

	setsHome, setsAway := ms.setsHome, ms.setsAway

	// Skip if match already decided
	if setsHome >= setsToWin || setsAway >= setsToWin {
		return
	}

	// Base Markov match-win probability (home perspective)
	homeFV := s.model.FairValue(
		setsHome, setsAway,
		p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints,
		p.Server, p.IsTiebreak,
	)

	// Per-set psychological adjustment
	adjustedHomeFV := s.applyPerSetAdjustment(homeFV, setsHome, setsAway, p.SetNumber, ms)
	adjustedAwayFV := 1.0 - adjustedHomeFV

	now := s.now()

	// Check both sides for edge
	s.mu.RLock()
	homePrice := s.prices[mkts[0]]
	homePriceTime := s.priceTimes[mkts[0]]
	awayPrice := s.prices[mkts[1]]
	awayPriceTime := s.priceTimes[mkts[1]]
	s.mu.RUnlock()

	homeEdge := int((adjustedHomeFV-homePrice)*100 + 1e-9)
	awayEdge := int((adjustedAwayFV-awayPrice)*100 + 1e-9)

	// Pick the side with larger edge
	var targetMkt string
	var targetFV, targetPrice float64
	var targetEdge int
	var targetPriceTime time.Time

	if homeEdge >= awayEdge {
		targetMkt = mkts[0]
		targetFV = adjustedHomeFV
		targetPrice = homePrice
		targetEdge = homeEdge
		targetPriceTime = homePriceTime
	} else {
		targetMkt = mkts[1]
		targetFV = adjustedAwayFV
		targetPrice = awayPrice
		targetEdge = awayEdge
		targetPriceTime = awayPriceTime
	}

	if targetPrice <= 0 {
		return
	}
	if now.Sub(targetPriceTime) > priceStaleTTL {
		return
	}
	if targetEdge < s.cfg.MinEdgeCents {
		return
	}
	if targetPrice < s.cfg.MinMarketPrice {
		return
	}
	if targetPrice > s.cfg.MaxMarketPrice {
		return
	}

	size := kellySized(targetFV, targetPrice)

	setWinProb := s.model.SetWinProbability(
		p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints,
		p.Server, p.IsTiebreak,
	)

	adjustment := adjustedHomeFV - homeFV
	payload, _ := json.Marshal(map[string]any{
		"set_number":     p.SetNumber,
		"sets_home":      setsHome,
		"sets_away":      setsAway,
		"home_games":     p.HomeGames,
		"away_games":     p.AwayGames,
		"home_points":    p.HomePoints,
		"away_points":    p.AwayPoints,
		"server":         p.Server,
		"is_tiebreak":    p.IsTiebreak,
		"markov_fv":      homeFV,
		"adjusted_fv":    adjustedHomeFV,
		"adjustment":     adjustment,
		"set_win_prob":   setWinProb,
		"target_side":    targetMkt == mkts[0],
		"target_fv":      targetFV,
		"target_price":   targetPrice,
		"target_edge":    targetEdge,
	})

	o := store.Order{
		TS:            now.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  targetMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("setwinner_s%d_%d-%d_edge%d", p.SetNumber, p.HomeGames, p.AwayGames, targetEdge),
		ConvProb:      targetFV,
		MarketPrice:   targetPrice,
		EdgeCents:     targetEdge,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("setwinner: order dropped", "match", eventTicker, "market", targetMkt)
		return
	}

	s.mu.Lock()
	ms.pointsSinceFire = 0
	s.mu.Unlock()

	s.log.Info("setwinner: order emitted",
		"match", eventTicker, "market", targetMkt,
		"set", p.SetNumber, "score", fmt.Sprintf("%d-%d", p.HomeGames, p.AwayGames),
		"markov_fv", homeFV, "adjusted_fv", adjustedHomeFV,
		"adjustment", adjustment,
		"price", targetPrice, "edge", targetEdge, "size", size)
}

// applyPerSetAdjustment modifies the Markov match-win probability based
// on per-set psychological effects from the research literature.
//
// Set 1 (0-0): no adjustment — i.i.d. holds (Depken et al. 2021).
// Set 2 (1-0 or 0-1): reversal — set 1 winner underperforms.
//   - If home won set 1: homeFV -= ReversalPenalty
//   - If away won set 1: homeFV += ReversalPenalty (away penalized)
// Set 3 (1-1 deciding): momentum — set 2 winner gets boost (IZA DP 9315).
//   - If home won set 2: homeFV += DecidingSetBoost
//   - If away won set 2: homeFV -= DecidingSetBoost
//
// set2Winner is derived from swMatchState: when set number transitions
// from 2 to 3, the previous set's game count tells us who won set 2.
func (s *SetWinnerStrategy) applyPerSetAdjustment(homeFV float64, setsHome, setsAway, setNum int, ms *swMatchState) float64 {
	if s.cfg.ReversalPenalty == 0 && s.cfg.DecidingSetBoost == 0 {
		return homeFV
	}

	// Set 1: no adjustment
	if setNum == 1 {
		return homeFV
	}

	// Set 2: reversal on set 1 winner
	if setNum == 2 {
		if setsHome > setsAway {
			// Home won set 1 — penalize home
			homeFV -= s.cfg.ReversalPenalty
		} else if setsAway > setsHome {
			// Away won set 1 — penalize away (home benefits)
			homeFV += s.cfg.ReversalPenalty
		}
		return clamp01(homeFV)
	}

	// Set 3 (deciding, 1-1): momentum for set 2 winner
	if setNum == 3 && setsHome == 1 && setsAway == 1 {
		if !ms.set2WinnerKnown {
			return homeFV
		}
		if ms.set2WinnerHome {
			homeFV += s.cfg.DecidingSetBoost
		} else {
			homeFV -= s.cfg.DecidingSetBoost
		}
		return clamp01(homeFV)
	}

	// Set 4+ (best-of-5): apply reversal to most recent set winner
	if setNum >= 4 {
		if setsHome > setsAway {
			homeFV -= s.cfg.ReversalPenalty
		} else if setsAway > setsHome {
			homeFV += s.cfg.ReversalPenalty
		}
		return clamp01(homeFV)
	}

	return homeFV
}

func clamp01(x float64) float64 {
	return math.Max(0.01, math.Min(0.99, x))
}

func (s *SetWinnerStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SetWinnerStrategy{%s: markets=%d, states=%d}",
		s.cfg.Label, len(s.markets), len(s.states))
}

func (s *SetWinnerStrategy) OnTick(ctx context.Context) {}
