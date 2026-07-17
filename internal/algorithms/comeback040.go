package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Comeback040Config controls the 0-40 comeback strategy.
// RQ11: at 0-40 on serve, market over-reacts — server's YES drops sharply.
// ATP players hold from 0-40 ~2% of the time, but the bet is on the MATCH
// outcome, not the game. Even if broken, the server can break back and win.
// The edge: market prices the server as if 0-40 = certain break, but
// match-win probability is higher than the depressed price implies.
//
// Betfair literature: "Back favourite at 0-40 down on serve. Price spikes
// to ~1.85, recovers to ~1.45 if hold. Win ~+12-14% of stake."
type Comeback040Config struct {
	// MinPeakPrice: server must have been priced above this (was favourite or competitive).
	// Filters out matches where server was already cheap (true underdog).
	MinPeakPrice float64
	// MaxEntryPrice: don't buy above this (avoid heavy favourites where 0-40 is real signal).
	MaxEntryPrice float64
	// MinEntryPrice: don't buy below this (avoid near-zero where recovery is unlikely).
	MinEntryPrice float64
	// ConvProb: estimated match-win probability from 0-40 state.
	// Conservative: 0.35 (server gets broken ~98% of the time, but can break back).
	ConvProb float64
	// BaseSize: order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultComeback040Config() Comeback040Config {
	return Comeback040Config{
		MinPeakPrice:   0.40,
		MaxEntryPrice:   0.70,
		MinEntryPrice:   0.10,
		ConvProb:        0.35,
		BaseSize:        10.0,
		Label:           "comeback040",
	}
}

// Comeback040Strategy buys the server's YES when they're down 0-40 on serve.
// The market over-reacts to 0-40, pricing the server as nearly certain to
// be broken. But match-win probability is higher than the depressed price
// implies — even if broken, the server can break back and win the match.
//
// Score-based: OnPoint detects 0-40 state on serve, buys the server's market.
// Also implements price-based detection as fallback: sharp drop from peak
// in a competitive match proxies a 0-40 situation.
type Comeback040Strategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	maxPrices map[string]float64
	markets   map[string][]string
	fired     map[string]bool
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       Comeback040Config
	replayNow *time.Time
}

func NewComeback040Strategy(emitter OrderEmitter, log *slog.Logger, cfg Comeback040Config) *Comeback040Strategy {
	return &Comeback040Strategy{
		prices:    make(map[string]float64),
		maxPrices: make(map[string]float64),
		markets:   make(map[string][]string),
		fired:     make(map[string]bool),
		emitter:   emitter,
		log:       log,
		cfg:       cfg,
	}
}

func (s *Comeback040Strategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *Comeback040Strategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.maxPrices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *Comeback040Strategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *Comeback040Strategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	if price > s.maxPrices[marketTicker] {
		s.maxPrices[marketTicker] = price
	}
	s.mu.Unlock()
}

// OnPoint detects 0-40 on serve and fires on the server's market.
func (s *Comeback040Strategy) OnPoint(eventTicker string, p store.Point) {
	// 0-40 on serve: server is down 0-40 (server's perspective)
	// home_points="0", away_points="40" when server=1 (home serving, down 0-40)
	// home_points="40", away_points="0" when server=2 (away serving, down 0-40)
	is040 := false
	serverMkt := ""

	if p.HomePoints == "0" && p.AwayPoints == "40" && p.Server == 1 {
		// Home serving, down 0-40 → buy home market
		is040 = true
	} else if p.HomePoints == "40" && p.AwayPoints == "0" && p.Server == 2 {
		// Away serving, down 0-40 → buy away market
		is040 = true
	}

	if !is040 {
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
	serverMkt = mkts[0]
	if p.Server == 2 {
		serverMkt = mkts[1]
	}

	price := s.prices[serverMkt]
	maxPrice := s.maxPrices[serverMkt]
	s.mu.Unlock()

	if price <= 0 {
		return
	}
	if maxPrice < s.cfg.MinPeakPrice {
		s.log.Debug("comeback040: skip, peak too low",
			"match", eventTicker, "peak", maxPrice, "min_peak", s.cfg.MinPeakPrice)
		return
	}
	if price > s.cfg.MaxEntryPrice {
		s.log.Debug("comeback040: skip, price too high",
			"match", eventTicker, "price", price, "max", s.cfg.MaxEntryPrice)
		return
	}
	if price < s.cfg.MinEntryPrice {
		s.log.Debug("comeback040: skip, price too low",
			"match", eventTicker, "price", price, "min", s.cfg.MinEntryPrice)
		return
	}

	edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
	if edgeCents < 1 {
		s.log.Debug("comeback040: skip, no edge",
			"match", eventTicker, "conv_prob", s.cfg.ConvProb, "price", price)
		return
	}

	ts := s.now()
	payload, _ := json.Marshal(map[string]any{
		"set":          p.SetNumber,
		"game":         p.GameNumber,
		"server":       p.Server,
		"home_points":  p.HomePoints,
		"away_points":  p.AwayPoints,
		"entry_price":  price,
		"max_price":    maxPrice,
		"conv_prob":    s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  serverMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("comeback040_set%d_game%d", p.SetNumber, p.GameNumber),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: s.cfg.BaseSize,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("comeback040: order dropped", "match", eventTicker, "market", serverMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("comeback040: order emitted",
		"match", eventTicker, "market", serverMkt,
		"set", p.SetNumber, "game", p.GameNumber,
		"price", price, "peak", maxPrice, "edge_cents", edgeCents)
}

func (s *Comeback040Strategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *Comeback040Strategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *Comeback040Strategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.maxPrices, marketTicker)
	s.mu.Unlock()
}

func (s *Comeback040Strategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("Comeback040Strategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

// PreMatchGated prevents pre-match price movements from triggering.
func (s *Comeback040Strategy) PreMatchGated() {}
