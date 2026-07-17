package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// TiebreakServerConfig controls the tiebreak server edge strategy.
// At 6-6 in a set, tiebreak begins. Server wins 60% of TB points vs
// 57% baseline. Markov assumes 50/50 at tiebreak. If market prices
// TB as coin flip, buying server's YES has ~10% edge.
type TiebreakServerConfig struct {
	// MinEdgeCents: minimum edge to fire (default 5)
	MinEdgeCents int
	// MaxMarketPrice: max price to buy at (default 0.65)
	MaxMarketPrice float64
	// ConvProb: estimated server win prob in tiebreak (0.60)
	ConvProb float64
	// BaseSize: order size in dollars
	BaseSize float64
	// Label: strategy label
	Label string
}

func DefaultTiebreakServerConfig() TiebreakServerConfig {
	return TiebreakServerConfig{
		MinEdgeCents:   5,
		MaxMarketPrice: 0.65,
		ConvProb:       0.60,
		BaseSize:       10.0,
		Label:          "tiebreak-server",
	}
}

// TiebreakServerStrategy buys the server's YES when a tiebreak starts.
// Empirical: server wins 60% of TB points. Markov assumes 50/50.
// Edge = (0.60 - market_price) * 100. Fires once per tiebreak per match.
type TiebreakServerStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool // per event
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       TiebreakServerConfig
	replayNow *time.Time
}

func NewTiebreakServerStrategy(emitter OrderEmitter, log *slog.Logger, cfg TiebreakServerConfig) *TiebreakServerStrategy {
	return &TiebreakServerStrategy{
		prices:  make(map[string]float64),
		markets: make(map[string][]string),
		fired:   make(map[string]bool),
		emitter: emitter,
		log:     log,
		cfg:     cfg,
	}
}

func (s *TiebreakServerStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *TiebreakServerStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *TiebreakServerStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *TiebreakServerStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.mu.Unlock()
}

// OnPoint detects tiebreak start (6-6, is_tiebreak=true) and fires.
func (s *TiebreakServerStrategy) OnPoint(eventTicker string, p store.Point) {
	// Fire on first tiebreak point
	if !p.IsTiebreak || p.PointNumber != 1 {
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

	// Server 1=home → market[0], Server 2=away → market[1]
	serverMkt := mkts[0]
	if p.Server == 2 {
		serverMkt = mkts[1]
	}
	price := s.prices[serverMkt]
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
		"set": p.SetNumber, "server": p.Server,
		"entry_price": price, "conv_prob": s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  serverMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("tbserver_set%d", p.SetNumber),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: s.cfg.BaseSize,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("tiebreak-server: order dropped", "match", eventTicker, "market", serverMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("tiebreak-server: order emitted",
		"match", eventTicker, "market", serverMkt,
		"set", p.SetNumber, "price", price, "edge_cents", edgeCents)
}

func (s *TiebreakServerStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *TiebreakServerStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *TiebreakServerStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *TiebreakServerStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("TiebreakServerStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

func (s *TiebreakServerStrategy) PreMatchGated() {}
