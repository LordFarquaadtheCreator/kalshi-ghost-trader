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

// Set1WinnerConfig controls the set-1 winner follow strategy.
// Empirical: set 1 winner wins match 72% (home 73%, away 71%).
// If market price for set 1 winner < 72c after set 1 ends, buy.
// Complements setdown (which buys the set 1 LOSER at depressed price).
type Set1WinnerConfig struct {
	// MinEdgeCents: minimum edge to fire (default 5)
	MinEdgeCents int
	// MaxMarketPrice: max price to buy at (default 0.72)
	MaxMarketPrice float64
	// ConvProb: P(set1 winner wins match) = 0.72
	ConvProb float64
	// BaseSize: order size in dollars
	BaseSize float64
	// Label: strategy label
	Label string
}

func DefaultSet1WinnerConfig() Set1WinnerConfig {
	return Set1WinnerConfig{
		MinEdgeCents:   5,
		MaxMarketPrice: 0.72,
		ConvProb:       0.72,
		BaseSize:       10.0,
		Label:          "set1winner",
	}
}

// Set1WinnerStrategy buys the set 1 winner's YES after set 1 ends.
// Fires when set_number transitions from 1 to 2 (set 1 complete).
// Edge = (0.72 - market_price) * 100.
type Set1WinnerStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       Set1WinnerConfig
	replayNow *time.Time
}

func NewSet1WinnerStrategy(emitter OrderEmitter, log *slog.Logger, cfg Set1WinnerConfig) *Set1WinnerStrategy {
	return &Set1WinnerStrategy{
		prices:  make(map[string]float64),
		markets: make(map[string][]string),
		fired:   make(map[string]bool),
		emitter: emitter,
		log:     log,
		cfg:     cfg,
	}
}

func (s *Set1WinnerStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *Set1WinnerStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *Set1WinnerStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *Set1WinnerStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.mu.Unlock()
}

// OnPoint detects set 1 completion (set_number transitions to 2).
func (s *Set1WinnerStrategy) OnPoint(eventTicker string, p store.Point) {
	// Fire on first point of set 2 (set 1 just ended)
	if p.SetNumber != 2 || p.PointNumber != 1 {
		return
	}

	s.mu.Lock()
	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}
	mkts, ok := s.markets[eventTicker]
	if !ok || len(mkts) < 2 {
		s.mu.Unlock()
		return
	}

	// Determine set 1 winner from set_games
	// home_set_games / away_set_games in the point record reflect
	// the completed set 1 score at the start of set 2
	homeWonSet1 := p.HomeSetGames > p.AwaySetGames
	winnerMkt := mkts[0] // home
	if !homeWonSet1 {
		winnerMkt = mkts[1] // away
	}
	price := s.prices[winnerMkt]
	s.mu.Unlock()

	if price <= 0 || price > s.cfg.MaxMarketPrice {
		return
	}

	edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
	if edgeCents < s.cfg.MinEdgeCents {
		return
	}

	ts := s.now()
	payload, _ := json.Marshal(map[string]any{
		"set1_home_games": p.HomeSetGames,
		"set1_away_games": p.AwaySetGames,
		"home_won_set1":   homeWonSet1,
		"entry_price":     price,
		"conv_prob":       s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  winnerMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("set1winner_s2start"),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: kellySized(s.cfg.ConvProb, price),
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     2,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("set1winner: order dropped", "match", eventTicker, "market", winnerMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("set1winner: order emitted",
		"match", eventTicker, "market", winnerMkt,
		"price", price, "edge_cents", edgeCents)
}

func (s *Set1WinnerStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *Set1WinnerStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *Set1WinnerStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *Set1WinnerStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("Set1WinnerStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

func (s *Set1WinnerStrategy) PreMatchGated() {}

func (s *Set1WinnerStrategy) OnTick(ctx context.Context) {}
