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

// DoubleBreakConfig controls the double break point strategy.
//
// Edge: at double break points (15-40, 30-40, 0-40), the returner's
// market is underpriced in the [0.20, 0.50] band. Empirical match-win
// rate is ~39% at avg price 0.35 → ~11% ROI, Sharpe 0.08.
//
// The market prices the returner as if the double break point is less
// valuable than it is. Break conversion rate is ~64%, and even when
// converted, the returner still needs to win the match. But the market
// over-discounts the returner at these price levels.
//
// One fire per (match, set, game) — avoids stacking bets within the
// same break point game (15-40 → 30-40 after server wins next point).
type DoubleBreakConfig struct {
	// MinEntryPrice: don't buy below this (near-zero = true underdog).
	MinEntryPrice float64
	// MaxEntryPrice: don't buy above this (above 0.50 edge disappears).
	MaxEntryPrice float64
	// ConvProb: estimated match-win probability from double break state.
	// Conservative: 0.38 (empirical 38.8% in [0.20, 0.50] band).
	ConvProb float64
	// MinEdgeCents: minimum edge to fire.
	MinEdgeCents int
	// BaseSize: order size in dollars (used when Kelly < 1).
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultDoubleBreakConfig() DoubleBreakConfig {
	return DoubleBreakConfig{
		MinEntryPrice: 0.20,
		MaxEntryPrice: 0.50,
		ConvProb:      0.38,
		MinEdgeCents:  1,
		BaseSize:      10.0,
		Label:         "doublebreak",
	}
}

// DoubleBreakStrategy buys the returner's YES at double break points
// (15-40, 30-40, 0-40) when the returner's market price is in the
// [MinEntryPrice, MaxEntryPrice] band.
//
// Score-based: OnPoint detects double break point state, buys returner's
// market. One fire per (match, set, game) to avoid stacking within the
// same break point game.
type DoubleBreakStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool // key: "match:set:game"
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       DoubleBreakConfig
	replayNow *time.Time
}

func NewDoubleBreakStrategy(emitter OrderEmitter, log *slog.Logger, cfg DoubleBreakConfig) *DoubleBreakStrategy {
	return &DoubleBreakStrategy{
		prices:  make(map[string]float64),
		markets: make(map[string][]string),
		fired:   make(map[string]bool),
		emitter: emitter,
		log:     log,
		cfg:     cfg,
	}
}

func (s *DoubleBreakStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *DoubleBreakStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	// Clean fired entries for this match
	prefix := eventTicker + ":"
	for k := range s.fired {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(s.fired, k)
		}
	}
	s.mu.Unlock()
}

func (s *DoubleBreakStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *DoubleBreakStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.mu.Unlock()
}

// OnPoint detects double break points and fires on the returner's market.
func (s *DoubleBreakStrategy) OnPoint(eventTicker string, p store.Point) {
	if p.IsTiebreak {
		return
	}

	// Determine returner (player not serving)
	returner := 2
	if p.Server == 2 {
		returner = 1
	}

	// Returner's points vs server's points
	var retPts, srvPts string
	if returner == 1 {
		retPts, srvPts = p.HomePoints, p.AwayPoints
	} else {
		retPts, srvPts = p.AwayPoints, p.HomePoints
	}

	// Double break point: returner has 40, server has 0/15/30
	isDoubleBP := false
	if retPts == "40" && (srvPts == "0" || srvPts == "15" || srvPts == "30") {
		isDoubleBP = true
	}

	if !isDoubleBP {
		return
	}

	// Dedup: one fire per (match, set, game)
	fireKey := fmt.Sprintf("%s:%d:%d", eventTicker, p.SetNumber, p.GameNumber)
	s.mu.Lock()
	if s.fired[fireKey] {
		s.mu.Unlock()
		return
	}
	mkts, ok := s.markets[eventTicker]
	if !ok || len(mkts) < 2 {
		s.mu.Unlock()
		return
	}

	returnerMkt := mkts[returner-1] // mkts[0]=home, mkts[1]=away
	price := s.prices[returnerMkt]
	s.mu.Unlock()

	if price <= 0 {
		return
	}
	if price < s.cfg.MinEntryPrice {
		s.log.Debug("doublebreak: skip, price below min",
			"match", eventTicker, "price", price, "min", s.cfg.MinEntryPrice)
		return
	}
	if price > s.cfg.MaxEntryPrice {
		s.log.Debug("doublebreak: skip, price above max",
			"match", eventTicker, "price", price, "max", s.cfg.MaxEntryPrice)
		return
	}

	edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
	if edgeCents < s.cfg.MinEdgeCents {
		s.log.Debug("doublebreak: skip, edge too small",
			"match", eventTicker, "conv_prob", s.cfg.ConvProb, "price", price, "edge", edgeCents)
		return
	}

	ts := s.now()
	payload, _ := json.Marshal(map[string]any{
		"set":         p.SetNumber,
		"game":        p.GameNumber,
		"server":      p.Server,
		"returner":    returner,
		"home_points": p.HomePoints,
		"away_points": p.AwayPoints,
		"ret_pts":     retPts,
		"srv_pts":     srvPts,
		"entry_price": price,
		"conv_prob":   s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  returnerMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("doublebreak_%s_set%d_game%d", retPts+"-"+srvPts, p.SetNumber, p.GameNumber),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: kellySized(s.cfg.ConvProb, price),
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("doublebreak: order dropped", "match", eventTicker, "market", returnerMkt)
		return
	}
	s.mu.Lock()
	s.fired[fireKey] = true
	s.mu.Unlock()
	s.log.Info("doublebreak: order emitted",
		"match", eventTicker, "market", returnerMkt,
		"set", p.SetNumber, "game", p.GameNumber,
		"score", retPts+"-"+srvPts, "price", price, "edge_cents", edgeCents)
}

func (s *DoubleBreakStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *DoubleBreakStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *DoubleBreakStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *DoubleBreakStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("DoubleBreakStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

// PreMatchGated prevents pre-match price movements from triggering.
func (s *DoubleBreakStrategy) PreMatchGated() {}

func (s *DoubleBreakStrategy) OnTick(ctx context.Context) {}
