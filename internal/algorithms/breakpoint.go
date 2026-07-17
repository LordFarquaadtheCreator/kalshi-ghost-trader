package algorithms

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// BreakPointStrategy buys the returner's market when they have a break point
// opportunity, using the Markov model for fair-value pricing.
//
// Logic: When the returner can win the game (break point), compute the Markov
// fair value for the returner. If market price < fair value - edge, buy.
// Sells on serve hold (price drops back).
type BreakPointStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64   // market_ticker → latest YES price
	priceTimes map[string]time.Time // market_ticker → last price update
	markets    map[string][]string  // event_ticker → [home, away] market tickers
	states     map[string]*bpMatchState
	emitter    OrderEmitter
	model      *MarkovModel
	cfg        BreakPointConfig
	log        *slog.Logger
	replayNow  *time.Time
}

type bpMatchState struct {
	setsHome int
	setsAway int
}

// BreakPointConfig configures the break-point strategy.
type BreakPointConfig struct {
	PServe         float64 // serve point win probability (default 0.64)
	MinEdgeCents   int     // minimum edge to trigger order
	MinMarketPrice float64 // don't buy below this price
	MaxMarketPrice float64 // don't buy above this price (0 = no cap)
	Label          string
}

// DefaultBreakPointConfig returns sensible defaults.
func DefaultBreakPointConfig() BreakPointConfig {
	return BreakPointConfig{
		PServe:         0.64,
		MinEdgeCents:   2,
		MinMarketPrice: 0.05,
		MaxMarketPrice: 0.60,
		Label:          "breakpoint",
	}
}

// NewBreakPointStrategy creates a break-point pricing strategy.
func NewBreakPointStrategy(emitter OrderEmitter, log *slog.Logger, cfg BreakPointConfig) *BreakPointStrategy {
	return &BreakPointStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		states:     make(map[string]*bpMatchState),
		emitter:    emitter,
		model:      NewMarkovModelWithProb(cfg.PServe),
		cfg:        cfg,
		log:        log,
	}
}

func (s *BreakPointStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

// OnPriceAt sets price with an explicit timestamp. Used by backtest.
func (s *BreakPointStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

// SetReplayTime sets the virtual "now" for staleness checks in backtest mode.
func (s *BreakPointStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *BreakPointStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *BreakPointStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	if _, ok := s.states[eventTicker]; !ok {
		s.states[eventTicker] = &bpMatchState{}
	}
	s.mu.Unlock()
}

func (s *BreakPointStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.states, eventTicker)
	s.mu.Unlock()
}

func (s *BreakPointStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *BreakPointStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)
	s.processBreakPoint(eventTicker, p)
}

func (s *BreakPointStrategy) updateMatchState(eventTicker string, p store.Point) {
	s.mu.Lock()
	ms := s.states[eventTicker]
	if ms == nil {
		ms = &bpMatchState{}
		s.states[eventTicker] = ms
	}
	// Track set count from HomeSetGames/AwaySetGames
	if p.HomeSetGames > ms.setsHome {
		ms.setsHome = p.HomeSetGames
	}
	if p.AwaySetGames > ms.setsAway {
		ms.setsAway = p.AwaySetGames
	}
	s.mu.Unlock()
}

func (s *BreakPointStrategy) processBreakPoint(eventTicker string, p store.Point) {
	if p.IsTiebreak {
		return
	}

	// Only act on break points
	pc := ClassifyPoint(PointContext{
		SetsHome:   s.getSetsHome(eventTicker),
		SetsAway:   s.getSetsAway(eventTicker),
		HomeGames:  p.HomeGames,
		AwayGames:  p.AwayGames,
		HomePoints: p.HomePoints,
		AwayPoints: p.AwayPoints,
		Server:     p.Server,
		IsTiebreak: p.IsTiebreak,
	})

	if !pc.IsBreakPoint {
		return
	}

	// Returner is the player not serving
	returner := 2
	if p.Server == 2 {
		returner = 1
	}

	s.mu.RLock()
	mkts, ok := s.markets[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mkts) < 2 {
		return
	}

	returnerMkt := mkts[returner-1] // mkts[0]=home, mkts[1]=away

	s.mu.RLock()
	price := s.prices[returnerMkt]
	priceTime := s.priceTimes[returnerMkt]
	s.mu.RUnlock()

	if price <= 0 || s.now().Sub(priceTime) > priceStaleTTL {
		return
	}

	// Compute Markov fair value for the returner
	// If returner is home (1), use home perspective; else away perspective
	var fv float64
	if returner == 1 {
		fv = s.model.FairValue(
			s.getSetsHome(eventTicker), s.getSetsAway(eventTicker),
			p.HomeGames, p.AwayGames,
			p.HomePoints, p.AwayPoints,
			p.Server, p.IsTiebreak,
		)
	} else {
		// Away perspective = 1 - home probability
		fv = 1.0 - s.model.FairValue(
			s.getSetsHome(eventTicker), s.getSetsAway(eventTicker),
			p.HomeGames, p.AwayGames,
			p.HomePoints, p.AwayPoints,
			p.Server, p.IsTiebreak,
		)
	}

	edgeCents := int((fv - price) * 100)

	if edgeCents < s.cfg.MinEdgeCents {
		s.log.Debug("breakpoint: edge too small",
			"event", eventTicker, "fv", fv, "price", price, "edge", edgeCents)
		return
	}
	if price < s.cfg.MinMarketPrice {
		return
	}
	if s.cfg.MaxMarketPrice > 0 && price > s.cfg.MaxMarketPrice {
		return
	}

	size := suggestedSize(edgeCents)

	s.emitter.EmitOrder(store.Order{
		MatchTicker:   eventTicker,
		MarketTicker:  returnerMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("break_point_%s_set%d_game%d", sideName(returner), p.SetNumber, p.GameNumber),
		ConvProb:      fv,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
	})

	s.log.Debug("breakpoint signal",
		"event", eventTicker, "market", returnerMkt,
		"fv", fv, "price", price, "edge", edgeCents,
		"server", p.Server, "returner", returner)
}

func (s *BreakPointStrategy) getSetsHome(eventTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms := s.states[eventTicker]; ms != nil {
		return ms.setsHome
	}
	return 0
}

func (s *BreakPointStrategy) getSetsAway(eventTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms := s.states[eventTicker]; ms != nil {
		return ms.setsAway
	}
	return 0
}

func (s *BreakPointStrategy) String() string {
	return fmt.Sprintf("BreakPointStrategy{markets=%d}", len(s.markets))
}

func sideName(player int) string {
	if player == 1 {
		return "home"
	}
	return "away"
}
