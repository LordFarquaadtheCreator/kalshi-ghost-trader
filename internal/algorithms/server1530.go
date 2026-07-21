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

// Server1530Config controls the 15-30 server edge strategy.
// At 15-30, market overreacts — ATP players hold 62% from 15-30, big servers 75%+.
// Market implies ~50%. Buy the server's YES at the small dip.
//
// Price-based detection: small dip (3-8%) from recent price on a favourite
// proxies a 15-30 pressure point.
type Server1530Config struct {
	// MinFavPrice: peak price must exceed this (must be favourite/server).
	MinFavPrice float64
	// MinDipPercent: minimum dip from previous price to trigger (0.03 = 3%).
	MinDipPercent float64
	// MaxDipPercent: maximum dip from previous price (0.08 = 8%).
	// Above this, it's likely a break not a 15-30.
	MaxDipPercent float64
	// MaxEntryPrice: don't buy above this price.
	MaxEntryPrice float64
	// ConvProb: estimated hold probability from 15-30 (ATP avg 0.62).
	ConvProb float64
	// BaseSize: order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultServer1530Config() Server1530Config {
	return Server1530Config{
		MinFavPrice:   0.55,
		MinDipPercent: 0.03,
		MaxDipPercent: 0.08,
		MaxEntryPrice: 0.65,
		ConvProb:      0.62,
		BaseSize:      10.0,
		Label:         "server1530",
	}
}

// Server1530Strategy buys a favourite's YES after a small price dip,
// betting the server holds from 15-30. Markets overreact to pressure points.
type Server1530Strategy struct {
	mu         sync.RWMutex
	prices     map[string]float64  // market_ticker -> latest price
	prevPrices map[string]float64  // market_ticker -> previous price
	maxPrices  map[string]float64  // market_ticker -> peak price
	markets    map[string][]string // event_ticker -> [home, away]
	fired      map[string]bool     // event_ticker -> fired
	emitter    OrderEmitter
	log        *slog.Logger
	cfg        Server1530Config
	replayNow  *time.Time
}

func NewServer1530Strategy(emitter OrderEmitter, log *slog.Logger, cfg Server1530Config) *Server1530Strategy {
	return &Server1530Strategy{
		prices:     make(map[string]float64),
		prevPrices: make(map[string]float64),
		maxPrices:  make(map[string]float64),
		markets:    make(map[string][]string),
		fired:      make(map[string]bool),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

func (s *Server1530Strategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *Server1530Strategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.prevPrices, mkt)
		delete(s.maxPrices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *Server1530Strategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *Server1530Strategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()

	prevPrice := s.prices[marketTicker]
	s.prevPrices[marketTicker] = prevPrice
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
	if maxPrice < s.cfg.MinFavPrice {
		s.mu.Unlock()
		return
	}

	// Need a previous price to detect dip
	if prevPrice <= 0 {
		s.mu.Unlock()
		return
	}

	dipPercent := (prevPrice - price) / prevPrice
	if dipPercent < s.cfg.MinDipPercent || dipPercent > s.cfg.MaxDipPercent {
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
		"max_price":   maxPrice,
		"prev_price":  prevPrice,
		"entry_price": price,
		"dip_percent": dipPercent,
		"conv_prob":   s.cfg.ConvProb,
		"entry_ts":    ts.UnixMilli(),
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       fmt.Sprintf("server1530_dip_%.1f%%", dipPercent*100),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: kellySized(s.cfg.ConvProb, price),
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("server1530: order dropped", "match", eventTicker, "market", marketTicker)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("server1530: order emitted",
		"match", eventTicker, "market", marketTicker,
		"price", price, "prev_price", prevPrice,
		"dip_pct", fmt.Sprintf("%.1f%%", dipPercent*100),
		"edge_cents", edgeCents)
}

// OnPoint fires when score reaches 15-30 on the server's market.
// Buys the server's YES at the depressed price, betting they hold.
func (s *Server1530Strategy) OnPoint(eventTicker string, p store.Point) {
	// Check for 15-30: server is behind 15-40 or 0-30 etc.
	// 15-30 means server has 15, returner has 30.
	homeIs15 := p.HomePoints == "15"
	awayIs30 := p.AwayPoints == "30"
	awayIs15 := p.AwayPoints == "15"
	homeIs30 := p.HomePoints == "30"

	is1530onHome := homeIs15 && awayIs30 && p.Server == 1
	is1530onAway := awayIs15 && homeIs30 && p.Server == 2
	if !is1530onHome && !is1530onAway {
		return
	}

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

	// Buy the server's market
	serverMkt := mkts[0] // home serves
	if p.Server == 2 {
		serverMkt = mkts[1]
	}

	maxPrice := s.maxPrices[serverMkt]
	price := s.prices[serverMkt]
	s.mu.Unlock()

	if maxPrice < s.cfg.MinFavPrice {
		return
	}
	if price <= 0 || price > s.cfg.MaxEntryPrice {
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
		"home_points": p.HomePoints,
		"away_points": p.AwayPoints,
		"server_mkt":  serverMkt,
		"max_price":   maxPrice,
		"entry_price": price,
		"conv_prob":   s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  serverMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("server1530_set%d_game%d", p.SetNumber, p.GameNumber),
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
		s.log.Warn("server1530: order dropped", "match", eventTicker, "market", serverMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("server1530: order emitted (score-based)",
		"match", eventTicker, "market", serverMkt,
		"set", p.SetNumber, "game", p.GameNumber,
		"score", p.HomePoints+"-"+p.AwayPoints,
		"price", price, "edge_cents", edgeCents)
}

func (s *Server1530Strategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *Server1530Strategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *Server1530Strategy) eventForMarket(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *Server1530Strategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.prevPrices, marketTicker)
	delete(s.maxPrices, marketTicker)
	s.mu.Unlock()
}

func (s *Server1530Strategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *Server1530Strategy) GetPriceAge(marketTicker string) time.Duration {
	return time.Hour
}

func (s *Server1530Strategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("Server1530Strategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

// PreMatchGated prevents pre-match price movements from triggering
// the price-based 15-30 path before real score data arrives.
func (s *Server1530Strategy) PreMatchGated() {}

func (s *Server1530Strategy) OnTick(ctx context.Context) {}
