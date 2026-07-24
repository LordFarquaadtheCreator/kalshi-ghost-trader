package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// SetPointConfig controls set-point strategy behavior.
//
// Original thesis: set points convert at 91% (93% serving, 89% returning)
// but markets price them at 56c avg — a 33c edge. That was wrong: the
// strategy compared set-conversion probability to a match-winner market
// price. Set conversion ≠ match-win probability.
//
// Fixed: uses Markov FairValue (match-win probability from current score
// state) for edge calculation. Set-point detection gates WHEN to fire;
// Markov determines IF there's an edge.
type SetPointConfig struct {
	// IncludeSetPoints: fire on non-match-point set points (set points in
	// sets that don't decide the match, e.g. set 1 when 0-0).
	IncludeSetPoints bool
	// IncludeReturning: fire when the set-point player is returning (breaking).
	// If false, only fire when serving.
	IncludeReturning bool
	// PServe: serve point win probability for Markov model (0.64 ATP, 0.62 WTA).
	PServe float64
	// MaxMarketPrice: skip signals above this price (0 = no cap).
	MaxMarketPrice float64
	// MinMarketPrice: skip signals below this price.
	MinMarketPrice float64
	// MinEdgeCents: minimum edge to emit order.
	MinEdgeCents int
	// CooldownPoints: minimum points between fires per match.
	CooldownPoints int
	// Label: strategy name for logging.
	Label string
	// MaxSetNumber: if > 0, only fire on set points in sets <= this number
	// (e.g. 1 = set 1 only). 0 = no filter.
	MaxSetNumber int
	// MinSetNumber: if > 0, only fire on set points in sets >= this number
	// (e.g. 2 = set 2+). 0 = no filter. Combine with MaxSetNumber for a range.
	MinSetNumber int
	// IncludeServing: if false, skip set points where the set-point player is
	// serving (returning-only mode). Default true via DefaultSetPointConfig.
	IncludeServing bool
	// SeriesFilter: if non-empty, only fire on events matching one of these
	// series tickers. Empty = no filter (all series).
	SeriesFilter []string
	// UTCHourStart / UTCHourEnd: if non-zero, only fire when entry ts UTC hour
	// falls in [Start, End). Both 0 = no filter.
	UTCHourStart int
	UTCHourEnd   int
}

// DefaultSetPointConfig fires on all set points (serving + returning).
// MinEdgeCents=5 clears Kalshi's ~1c/contract fee with margin.
func DefaultSetPointConfig() SetPointConfig {
	return SetPointConfig{
		IncludeSetPoints: true,
		IncludeReturning: true,
		IncludeServing:   true,
		PServe:           0.64,
		MaxMarketPrice:   0.0,
		MinMarketPrice:   0.05,
		MinEdgeCents:     5,
		CooldownPoints:   3,
		Label:            "setpoint",
	}
}

// spMatchState tracks per-event set tracking, point dedup, and cooldown.
type spMatchState struct {
	setsHome        int
	setsAway        int
	lastSetNum      int
	lastHomeGames   int
	lastAwayGames   int
	lastScorer      int
	pointsSinceFire int
}

// SetPointStrategy is a configurable set-point detection strategy.
// Uses Markov match-win probability for edge calculation, gated to fire
// only at set points (including tiebreak set points).
type SetPointStrategy struct {
	mu          sync.RWMutex
	prices      map[string]float64
	priceTimes  map[string]time.Time
	markets     map[string][]string
	matchStates map[string]*spMatchState
	seenPoints  map[string]map[string]bool
	series      map[string]string // event_ticker -> series_ticker
	emitter     OrderEmitter
	model       *MarkovModel
	db          *store.DB // nil in backtest mode
	log         *slog.Logger
	cfg         SetPointConfig
	replayNow   *time.Time
}

func NewSetPointStrategy(emitter OrderEmitter, log *slog.Logger, cfg SetPointConfig) *SetPointStrategy {
	return &SetPointStrategy{
		prices:      make(map[string]float64),
		priceTimes:  make(map[string]time.Time),
		markets:     make(map[string][]string),
		matchStates: make(map[string]*spMatchState),
		seenPoints:  make(map[string]map[string]bool),
		series:      make(map[string]string),
		emitter:     emitter,
		model:       NewMarkovModelWithProb(cfg.PServe),
		log:         log,
		cfg:         cfg,
	}
}

// NewSetPointStrategyWithDB creates a live-mode setpoint that auto-loads
// series_ticker from the markets table on RegisterMarkets.
func NewSetPointStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg SetPointConfig) *SetPointStrategy {
	s := NewSetPointStrategy(emitter, log, cfg)
	s.db = db
	return s
}

// SetSharedMarkovModel replaces the per-strategy model with a shared one.
// Memoization then works across strategies with identical pServe.
func (s *SetPointStrategy) SetSharedMarkovModel(m *MarkovModel) {
	s.mu.Lock()
	s.model = m
	s.mu.Unlock()
}

// SetSeriesTicker maps event_ticker to series_ticker for series filtering.
// Implements SeriesSetter — called by backtest engine or live wiring.
func (s *SetPointStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

func (s *SetPointStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	if _, ok := s.matchStates[eventTicker]; !ok {
		s.matchStates[eventTicker] = &spMatchState{}
	}
	s.mu.Unlock()

	if s.db != nil {
		s.loadSeriesTicker(eventTicker)
	}
}

func (s *SetPointStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
}

func (s *SetPointStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.matchStates, eventTicker)
	delete(s.seenPoints, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *SetPointStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *SetPointStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *SetPointStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SetPointStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SetPointStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)
	s.processPoint(eventTicker, p)
}

func (s *SetPointStrategy) updateMatchState(eventTicker string, p store.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.matchStates[eventTicker]
	if !ok {
		ms = &spMatchState{}
		s.matchStates[eventTicker] = ms
	}
	if p.SetNumber > ms.lastSetNum && ms.lastSetNum > 0 {
		if ms.lastHomeGames > ms.lastAwayGames {
			ms.setsHome++
		} else if ms.lastAwayGames > ms.lastHomeGames {
			ms.setsAway++
		} else if ms.lastScorer != 0 {
			if ms.lastScorer == 1 {
				ms.setsHome++
			} else {
				ms.setsAway++
			}
		}
	}
	ms.lastSetNum = p.SetNumber
	ms.lastHomeGames = p.HomeGames
	ms.lastAwayGames = p.AwayGames
	ms.lastScorer = p.Scorer
	ms.pointsSinceFire++
}

func (s *SetPointStrategy) processPoint(eventTicker string, p store.Point) {
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

	sp := s.detectSetPoint(eventTicker, p)
	if sp == nil {
		return
	}

	// MaxSetNumber filter: only fire on sets <= MaxSetNumber
	if s.cfg.MaxSetNumber > 0 && p.SetNumber > s.cfg.MaxSetNumber {
		return
	}
	// MinSetNumber filter: only fire on sets >= MinSetNumber
	if s.cfg.MinSetNumber > 0 && p.SetNumber < s.cfg.MinSetNumber {
		return
	}

	// Series filter
	if !seriesMatches(s.series[eventTicker], s.cfg.SeriesFilter) {
		return
	}

	// UTC hour filter
	if !utcHourMatches(s.now(), s.cfg.UTCHourStart, s.cfg.UTCHourEnd) {
		return
	}

	isServing := (sp.winner == 1 && p.Server == 1) || (sp.winner == 2 && p.Server == 2)
	if !isServing && !s.cfg.IncludeReturning {
		return
	}
	if isServing && !s.cfg.IncludeServing {
		return
	}

	// Cooldown check
	s.mu.RLock()
	ms := s.matchStates[eventTicker]
	cooldownOK := ms != nil && ms.pointsSinceFire >= s.cfg.CooldownPoints
	setsHome, setsAway := 0, 0
	if ms != nil {
		setsHome = ms.setsHome
		setsAway = ms.setsAway
	}
	s.mu.RUnlock()
	if !cooldownOK {
		return
	}

	s.mu.RLock()
	mktTickers, ok := s.markets[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mktTickers) < 2 {
		return
	}

	var marketTicker string
	if sp.winner == 1 {
		marketTicker = mktTickers[0]
	} else {
		marketTicker = mktTickers[1]
	}

	// Markov match-win probability for the set-point winner.
	// FairValue returns P(home wins match) — flip for away.
	homeFV := s.model.FairValue(
		setsHome, setsAway,
		p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints,
		p.Server, p.IsTiebreak,
	)
	var convProb float64
	if sp.winner == 1 {
		convProb = homeFV
	} else {
		convProb = 1.0 - homeFV
	}

	s.mu.RLock()
	mktPrice := s.prices[marketTicker]
	priceTime := s.priceTimes[marketTicker]
	s.mu.RUnlock()
	if mktPrice <= 0 {
		return
	}
	if mktPrice < s.cfg.MinMarketPrice {
		return
	}
	if s.cfg.MaxMarketPrice > 0 && mktPrice > s.cfg.MaxMarketPrice {
		return
	}
	age := s.now().Sub(priceTime)
	if age > priceStaleTTL {
		return
	}

	edgeCents := int((convProb-mktPrice)*100 + 1e-9)
	if edgeCents < s.cfg.MinEdgeCents {
		return
	}

	size := kellySized(convProb, mktPrice)

	payload, _ := json.Marshal(map[string]any{
		"home_games":   p.HomeGames, "away_games": p.AwayGames,
		"home_points":  p.HomePoints, "away_points": p.AwayPoints,
		"server":       p.Server, "scorer": p.Scorer,
		"set":          p.SetNumber, "game": p.GameNumber,
		"serving":      isServing,
		"is_mp":        sp.isMatchPoint,
		"is_tiebreak":  p.IsTiebreak,
		"markov_fv":    homeFV,
		"sets_home":    setsHome, "sets_away": setsAway,
	})

	o := store.Order{
		TS:            s.now().UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       sp.context,
		ConvProb:      convProb,
		MarketPrice:   mktPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("setpoint: order dropped", "match", eventTicker, "market", marketTicker)
		return
	}

	s.mu.Lock()
	if ms != nil {
		ms.pointsSinceFire = 0
	}
	s.mu.Unlock()

	s.log.Info("setpoint: order emitted",
		"match", eventTicker, "market", marketTicker,
		"action", "buy", "edge_cents", edgeCents, "conv_prob", convProb,
		"mkt_price", mktPrice, "size", size, "context", sp.context,
		"serving", isServing, "is_mp", sp.isMatchPoint, "is_tb", p.IsTiebreak)
}

type setPointSignal struct {
	winner       int
	context      string
	isMatchPoint bool
}

func (s *SetPointStrategy) detectSetPoint(eventTicker string, p store.Point) *setPointSignal {
	s.mu.RLock()
	ms := s.matchStates[eventTicker]
	var setsHome, setsAway int
	if ms != nil {
		setsHome = ms.setsHome
		setsAway = ms.setsAway
	}
	s.mu.RUnlock()

	homeNeedsSet := setsToWin - setsHome
	awayNeedsSet := setsToWin - setsAway
	if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
		return nil
	}

	homeOneSetAway := homeNeedsSet == 1
	awayOneSetAway := awayNeedsSet == 1

	// Tiebreak: set point = one point away from winning tiebreak (first to 7, win by 2).
	if p.IsTiebreak {
		h := tbPointValue(p.HomePoints)
		a := tbPointValue(p.AwayPoints)
		// Win next point → score+1 >= 7 AND lead >= 2
		homeCanWinSet := h >= 6 && (h-a) >= 1
		awayCanWinSet := a >= 6 && (a-h) >= 1
		if !homeCanWinSet && !awayCanWinSet {
			return nil
		}
		homeIsMP := homeCanWinSet && homeOneSetAway
		awayIsMP := awayCanWinSet && awayOneSetAway
		if !s.cfg.IncludeSetPoints && !homeIsMP && !awayIsMP {
			return nil
		}
		winner := 2
		ctx := "away_tb_set_point"
		if homeCanWinSet {
			winner = 1
			ctx = "home_tb_set_point"
		}
		if homeIsMP {
			ctx = "home_tb_match_point"
		} else if awayIsMP {
			ctx = "away_tb_match_point"
		}
		return &setPointSignal{
			winner:       winner,
			context:      ctx,
			isMatchPoint: homeIsMP || awayIsMP,
		}
	}

	// Non-tiebreak: set point = can win this game to win the set.
	homeCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 1)
	awayCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 2)

	homeCanWinSet := homeCanWinGame && p.HomeGames >= gamesPerSet-1 && p.HomeGames > p.AwayGames
	awayCanWinSet := awayCanWinGame && p.AwayGames >= gamesPerSet-1 && p.AwayGames > p.HomeGames

	if !homeCanWinSet && !awayCanWinSet {
		return nil
	}

	homeIsMP := homeCanWinSet && homeOneSetAway
	awayIsMP := awayCanWinSet && awayOneSetAway

	if !s.cfg.IncludeSetPoints && !homeIsMP && !awayIsMP {
		return nil
	}

	winner := 2
	ctx := "away_set_point"
	if homeCanWinSet {
		winner = 1
		ctx = "home_set_point"
	}
	if homeIsMP {
		ctx = "home_match_point"
	} else if awayIsMP {
		ctx = "away_match_point"
	}

	return &setPointSignal{
		winner:       winner,
		context:      ctx,
		isMatchPoint: homeIsMP || awayIsMP,
	}
}

func (s *SetPointStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *SetPointStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *SetPointStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *SetPointStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SetPointStrategy{%s: markets=%d, prices=%d, states=%d}",
		s.cfg.Label, len(s.markets), len(s.prices), len(s.matchStates))
}

func (s *SetPointStrategy) OnTick(ctx context.Context) {}
