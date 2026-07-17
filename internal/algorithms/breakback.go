package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// BreakBackConfig controls the break-back fade strategy.
// After a break of serve, markets overreact — broken player's YES drops
// sharply. Break-back rate ~25-30%. Buy the broken player at depressed price.
//
// Price-based detection: sharp drop (>MinDropPercent from peak) proxies a break.
type BreakBackConfig struct {
	// MinDropPercent: minimum price drop from peak to trigger entry (0-1).
	// 0.15 = 15% drop from max price seen.
	MinDropPercent float64
	// MinPeakPrice: peak price must exceed this to confirm meaningful market.
	// Filters out matches where player was already cheap.
	MinPeakPrice float64
	// MaxEntryPrice: don't buy above this price (avoid heavy favourites).
	MaxEntryPrice float64
	// ConvProb: estimated win probability after break (recovery rate).
	ConvProb float64
	// BaseSize: order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultBreakBackConfig() BreakBackConfig {
	return BreakBackConfig{
		MinDropPercent: 0.15,
		MinPeakPrice:   0.30,
		MaxEntryPrice:  0.55,
		ConvProb:       0.55,
		BaseSize:       10.0,
		Label:          "breakback",
	}
}

// BreakBackStrategy buys a player's YES after a break of serve.
// Markets overreact to breaks — broken player's YES drops sharply.
// Break-back rate ~25-30%. Buy the broken player at depressed price.
//
// Score-based: OnPoint detects actual break (scorer != server), then
// buys the broken player's market at current price.
type BreakBackStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64  // market_ticker -> latest price
	maxPrices map[string]float64  // market_ticker -> peak price seen
	markets   map[string][]string // event_ticker -> [home, away]
	fired     map[string]bool     // event_ticker -> fired
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       BreakBackConfig
	replayNow *time.Time
}

func NewBreakBackStrategy(emitter OrderEmitter, log *slog.Logger, cfg BreakBackConfig) *BreakBackStrategy {
	return &BreakBackStrategy{
		prices:    make(map[string]float64),
		maxPrices: make(map[string]float64),
		markets:   make(map[string][]string),
		fired:     make(map[string]bool),
		emitter:   emitter,
		log:       log,
		cfg:       cfg,
	}
}

func (s *BreakBackStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *BreakBackStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.maxPrices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *BreakBackStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *BreakBackStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	if price > s.maxPrices[marketTicker] {
		s.maxPrices[marketTicker] = price
	}

	eventTicker := s.eventForMarket(marketTicker)
	if eventTicker == "" || s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	maxPrice := s.maxPrices[marketTicker]
	if maxPrice < s.cfg.MinPeakPrice {
		s.mu.Unlock()
		return
	}

	dropPercent := (maxPrice - price) / maxPrice
	if dropPercent < s.cfg.MinDropPercent {
		s.mu.Unlock()
		return
	}

	if price > s.cfg.MaxEntryPrice {
		s.mu.Unlock()
		return
	}

	s.mu.Unlock()

	edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"max_price":    maxPrice,
		"entry_price":  price,
		"drop_percent": dropPercent,
		"conv_prob":    s.cfg.ConvProb,
		"entry_ts":     ts.UnixMilli(),
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       fmt.Sprintf("breakback_drop_%.0f%%", dropPercent*100),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: s.cfg.BaseSize,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("breakback: order dropped", "match", eventTicker, "market", marketTicker)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("breakback: order emitted",
		"match", eventTicker, "market", marketTicker,
		"price", price, "max_price", maxPrice,
		"drop_pct", fmt.Sprintf("%.1f%%", dropPercent*100),
		"edge_cents", edgeCents)
}

// OnPoint detects actual break of serve from score data and fires on
// the broken player's market at current price.
func (s *BreakBackStrategy) OnPoint(eventTicker string, p store.Point) {
	if p.IsBreakPoint && p.Scorer != p.Server {
		s.mu.Lock()
		if s.fired[eventTicker] {
			s.mu.Unlock()
			return
		}
		mkts, ok := s.markets[eventTicker]
		if !ok {
			s.mu.Unlock()
			return
		}
		// scorer != server means server was broken.
		// Server 1=home -> broken player is home -> market[0].
		// Server 2=away -> broken player is away -> market[1].
		brokenMkt := mkts[0]
		if p.Server == 2 {
			brokenMkt = mkts[1]
		}
		price := s.prices[brokenMkt]
		maxPrice := s.maxPrices[brokenMkt]
		s.mu.Unlock()

		if price <= 0 || price > s.cfg.MaxEntryPrice {
			return
		}
		if maxPrice < s.cfg.MinPeakPrice {
			return
		}

		edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
		if edgeCents < 1 {
			return
		}

		ts := s.now()
		payload, _ := json.Marshal(map[string]any{
			"set":         p.SetNumber,
			"game":        p.GameNumber,
			"server":      p.Server,
			"scorer":      p.Scorer,
			"broken_mkt":  brokenMkt,
			"entry_price": price,
			"max_price":   maxPrice,
			"conv_prob":   s.cfg.ConvProb,
		})

		o := store.Order{
			TS:            ts.UnixMilli(),
			MatchTicker:   eventTicker,
			MarketTicker:  brokenMkt,
			Action:        "buy",
			Context:       fmt.Sprintf("breakback_set%d_game%d", p.SetNumber, p.GameNumber),
			ConvProb:      s.cfg.ConvProb,
			MarketPrice:   price,
			EdgeCents:     edgeCents,
			SuggestedSize: s.cfg.BaseSize,
			SetNumber:     p.SetNumber,
			Strategy:      s.cfg.Label,
			Payload:       string(payload),
		}

		if !s.emitter.EmitOrder(o) {
			s.log.Warn("breakback: order dropped", "match", eventTicker, "market", brokenMkt)
			return
		}
		s.mu.Lock()
		s.fired[eventTicker] = true
		s.mu.Unlock()
		s.log.Info("breakback: order emitted (score-based)",
			"match", eventTicker, "market", brokenMkt,
			"set", p.SetNumber, "game", p.GameNumber,
			"price", price, "edge_cents", edgeCents)
	}
}

func (s *BreakBackStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *BreakBackStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *BreakBackStrategy) eventForMarket(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *BreakBackStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.maxPrices, marketTicker)
	s.mu.Unlock()
}

func (s *BreakBackStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *BreakBackStrategy) GetPriceAge(marketTicker string) time.Duration {
	return time.Hour
}

func (s *BreakBackStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("BreakBackStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

// PreMatchGated prevents pre-match price movements from triggering
// the price-based break-back path before real score data arrives.
func (s *BreakBackStrategy) PreMatchGated() {}
