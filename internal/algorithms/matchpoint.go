package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

const (
	minEdgeCents   = 1
	baseSize       = 10.0
	maxSize        = 100.0
	setsToWin      = 2
	gamesPerSet    = 6
	priceStaleTTL  = 60 * time.Second
	minMarketPrice = 0.05

	// Empirical conversion rate: when serving for the match, players convert
	// 97.3% of the time (backtest: research/strategy_analysis/match_point_edge.py).
	// Returning (breaking for match): 88.5% — higher edge but higher variance.
	// We only fire on serve match points — cleaner signal, lower variance.
	serveConvProb = 0.97
)

// matchState tracks per-match set scores derived from point flow.
// FlashScore points arrive sequentially; when SetNumber increments,
// the previous set's final game scores determine who won that set.
type matchState struct {
	setsHome      int
	setsAway      int
	lastSetNum    int
	lastHomeGames int
	lastAwayGames int
	lastScorer    int // 1=home, 2=away — last point scorer (for tiebreak set winner)
}

// MatchPointStrategy detects match points from point data and emits
// buy orders when the edge exceeds the threshold. Implements both
// Strategy and PriceLookup.
type MatchPointStrategy struct {
	mu          sync.RWMutex
	prices      map[string]float64         // market_ticker -> latest YES price (0-1)
	priceTimes  map[string]time.Time       // market_ticker -> last price update
	markets     map[string][]string        // event_ticker -> [home_ticker, away_ticker]
	matchStates map[string]*matchState     // event_ticker -> set tracking state
	seenPoints  map[string]map[string]bool // event_ticker -> dedup set ("set:game:point")
	emitter     OrderEmitter
	log         *slog.Logger

	// replayNow, when non-nil, overrides time.Now() for staleness checks.
	// Set by backtest to the timestamp of the point being processed.
	replayNow *time.Time
}

// NewMatchPointStrategy creates a match-point detection strategy.
// emitter receives simulated buy orders. Use TickWriterEmitter for live
// or OrderCollector for backtest.
func NewMatchPointStrategy(emitter OrderEmitter, log *slog.Logger) *MatchPointStrategy {
	return &MatchPointStrategy{
		prices:      make(map[string]float64),
		priceTimes:  make(map[string]time.Time),
		markets:     make(map[string][]string),
		matchStates: make(map[string]*matchState),
		seenPoints:  make(map[string]map[string]bool),
		emitter:     emitter,
		log:         log,
	}
}

func (s *MatchPointStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

// UnregisterMarkets removes all state for a match — markets, set tracking,
// dedup cache, and prices for the associated market tickers.
func (s *MatchPointStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.matchStates, eventTicker)
	delete(s.seenPoints, eventTicker)
	s.mu.Unlock()
}

func (s *MatchPointStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

// OnPriceAt sets price with an explicit timestamp. Used by backtest
// to replay historical ticks with correct staleness checking.
func (s *MatchPointStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

// SetReplayTime sets the virtual "now" for staleness checks in backtest mode.
// Pass time.Time{} (zero) to disable replay mode and use wall clock again.
func (s *MatchPointStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

// now returns the effective current time — replay time if set, wall clock otherwise.
func (s *MatchPointStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *MatchPointStrategy) OnPoints(pts []store.Point) {
	for _, p := range pts {
		s.updateMatchState(p)
		s.processPoint(p)
	}
}

// updateMatchState tracks set scores by detecting set transitions.
// When SetNumber increments, the previous set's final game scores
// (from the last point seen) determine the set winner.
func (s *MatchPointStrategy) updateMatchState(p store.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.matchStates[p.MatchTicker]
	if !ok {
		ms = &matchState{}
		s.matchStates[p.MatchTicker] = ms
	}
	if p.SetNumber > ms.lastSetNum && ms.lastSetNum > 0 {
		if ms.lastHomeGames > ms.lastAwayGames {
			ms.setsHome++
		} else if ms.lastAwayGames > ms.lastHomeGames {
			ms.setsAway++
		} else if ms.lastScorer != 0 {
			// Tiebreak set: game counts equal, winner is last point's scorer
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
}

func (s *MatchPointStrategy) processPoint(p store.Point) {
	pointKey := fmt.Sprintf("%d:%d:%d", p.SetNumber, p.GameNumber, p.PointNumber)
	s.mu.Lock()
	if s.seenPoints[p.MatchTicker] == nil {
		s.seenPoints[p.MatchTicker] = make(map[string]bool)
	}
	if s.seenPoints[p.MatchTicker][pointKey] {
		s.mu.Unlock()
		return
	}
	s.seenPoints[p.MatchTicker][pointKey] = true
	s.mu.Unlock()

	mp := s.detectMatchPoint(p)
	if mp == nil {
		return
	}

	// Only fire when the match-point player is serving.
	// Backtest: serving MP converts 97.3%, returning MP 88.5% but high variance.
	// mp.winner == 1 means home has MP; server == 1 means home is serving.
	isServing := (mp.winner == 1 && p.Server == 1) || (mp.winner == 2 && p.Server == 2)
	if !isServing {
		s.log.Debug("matchpoint: MP but not serving, skipping",
			"match", p.MatchTicker, "winner", mp.winner, "server", p.Server,
			"context", mp.context)
		return
	}

	s.mu.RLock()
	mktTickers, ok := s.markets[p.MatchTicker]
	s.mu.RUnlock()
	if !ok || len(mktTickers) < 2 {
		return
	}

	var marketTicker string
	if mp.winner == 1 {
		marketTicker = mktTickers[0]
	} else {
		marketTicker = mktTickers[1]
	}

	convProb := serveConvProb

	s.mu.RLock()
	mktPrice := s.prices[marketTicker]
	priceTime := s.priceTimes[marketTicker]
	s.mu.RUnlock()
	if mktPrice <= 0 {
		s.log.Debug("matchpoint: no price for market", "market", marketTicker)
		return
	}
	if mktPrice < minMarketPrice {
		s.log.Debug("matchpoint: price below min filter", "market", marketTicker, "price", mktPrice)
		return
	}
	age := s.now().Sub(priceTime)
	if age > priceStaleTTL {
		s.log.Debug("matchpoint: stale price", "market", marketTicker, "age", age)
		return
	}

	edgeCents := int((convProb-mktPrice)*100 + 1e-9)
	if edgeCents < minEdgeCents {
		return
	}

	// Buy only — never sell at match points.
	// Backtest: selling (comeback bets) has 7.1% hit rate, catastrophic PnL.
	size := suggestedSize(edgeCents)

	payload, _ := json.Marshal(map[string]any{
		"home_games": p.HomeGames, "away_games": p.AwayGames,
		"home_points": p.HomePoints, "away_points": p.AwayPoints,
		"server": p.Server, "scorer": p.Scorer,
		"set": p.SetNumber, "game": p.GameNumber,
		"serving": true,
	})

	o := store.Order{
		TS:            time.Now().UnixMilli(),
		MatchTicker:   p.MatchTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       mp.context,
		ConvProb:      convProb,
		MarketPrice:   mktPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     p.SetNumber,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("matchpoint: order dropped", "match", p.MatchTicker, "market", marketTicker)
		return
	}
	s.log.Info("matchpoint: order emitted",
		"match", p.MatchTicker, "market", marketTicker,
		"action", "buy", "edge_cents", edgeCents, "conv_prob", convProb,
		"mkt_price", mktPrice, "size", size, "context", mp.context)
}

type matchPoint struct {
	winner  int // 1=home, 2=away
	context string
}

func (s *MatchPointStrategy) detectMatchPoint(p store.Point) *matchPoint {
	s.mu.RLock()
	ms := s.matchStates[p.MatchTicker]
	var setsHome, setsAway int
	if ms != nil {
		setsHome = ms.setsHome
		setsAway = ms.setsAway
	}
	s.mu.RUnlock()
	gamesHome, gamesAway := p.HomeGames, p.AwayGames

	homeNeedsSet := setsToWin - setsHome
	awayNeedsSet := setsToWin - setsAway
	if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
		return nil
	}

	homeOneSetAway := homeNeedsSet == 1
	awayOneSetAway := awayNeedsSet == 1

	var homeMatchPoint, awayMatchPoint bool

	// Skip tiebreak points — research decision: don't factor in tiebreakers.
	// Set counting still handles TB sets via lastScorer in updateMatchState.
	if p.IsTiebreak {
		return nil
	}

	// Regular game: receiver can win game with next point
	homeCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 1)
	awayCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 2)

	if homeOneSetAway && homeCanWinGame && gamesHome >= gamesPerSet-1 && gamesHome > gamesAway {
		homeMatchPoint = true
	}
	if awayOneSetAway && awayCanWinGame && gamesAway >= gamesPerSet-1 && gamesAway > gamesHome {
		awayMatchPoint = true
	}

	if !homeMatchPoint && !awayMatchPoint {
		return nil
	}

	winner := 2
	ctx := "away_match_point"
	if homeMatchPoint {
		winner = 1
		ctx = "home_match_point"
	}

	return &matchPoint{
		winner:  winner,
		context: ctx,
	}
}

func canWinGame(homePts, awayPts string, server, player int) bool {
	h := normalizeScore(homePts)
	a := normalizeScore(awayPts)
	if player == 1 {
		// Home can win game with next point if: advantage, or 40 vs <40
		return h == "A" || (h == "40" && a != "40" && a != "A")
	}
	return a == "A" || (a == "40" && h != "40" && h != "A")
}

func normalizeScore(s string) string {
	switch s {
	case "0", "15", "30", "40", "A":
		return s
	default:
		return ""
	}
}

func suggestedSize(absEdgeCents int) float64 {
	size := baseSize * float64(absEdgeCents) / float64(minEdgeCents)
	if size > maxSize {
		size = maxSize
	}
	return size
}

// DeletePrice removes a single market's price tracking state.
// Called by tracker on unsubscribe to prevent unbounded growth when
// FlashScore is disabled (UnregisterMarkets is only called from FlashScore).
func (s *MatchPointStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *MatchPointStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

// GetPriceAge returns how long ago the price was last updated.
// Returns a large duration if no price exists (stale/missing).
func (s *MatchPointStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *MatchPointStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("MatchPointStrategy{markets=%d, prices=%d, states=%d}",
		len(s.markets), len(s.prices), len(s.matchStates))
}
