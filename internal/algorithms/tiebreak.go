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

// TiebreakConfig controls the tiebreak mini-break fade strategy.
// In tiebreaks, mini-breaks are mean-reverting (~52% returned).
// Market overprices the first mini-break — buy the mini-broken player.
//
// Price-based detection: sharp dip in a narrow price band (0.35-0.65)
// with high volatility proxies a tiebreak mini-break swing.
type TiebreakConfig struct {
	// MinPeakPrice: peak price must exceed this (both players competitive).
	MinPeakPrice float64
	// MinDropPercent: minimum drop from peak to trigger (0.10 = 10%).
	MinDropPercent float64
	// MaxDropPercent: maximum drop from peak (0.25 = 25%).
	// Above this, likely a real break not a mini-break.
	MaxDropPercent float64
	// MinEntryPrice: don't buy below this (avoid near-zero).
	MinEntryPrice float64
	// MaxEntryPrice: don't buy above this (avoid heavy favourite).
	MaxEntryPrice float64
	// ConvProb: estimated reversion probability (0.52 = mini-break return rate).
	ConvProb float64
	// BaseSize: order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
	// SeriesFilter: if non-empty, only fire on events matching one of these
	// series tickers. Empty = no filter (all series).
	SeriesFilter []string
	// UTCHourStart / UTCHourEnd: if non-zero, only fire when entry ts UTC hour
	// falls in [Start, End). Both 0 = no filter.
	UTCHourStart int
	UTCHourEnd   int
}

func DefaultTiebreakConfig() TiebreakConfig {
	return TiebreakConfig{
		MinPeakPrice:   0.40,
		MinDropPercent: 0.10,
		MaxDropPercent: 0.25,
		MinEntryPrice:  0.25,
		MaxEntryPrice:  0.60,
		ConvProb:       0.52,
		BaseSize:       10.0,
		Label:          "tiebreak",
	}
}

// TiebreakStrategy buys a player's YES after a sharp dip in a narrow band,
// betting on mini-break reversion. Tiebreaks are high-volatility, mean-reverting.
type TiebreakStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64  // market_ticker -> latest price
	maxPrices map[string]float64  // market_ticker -> peak price
	markets   map[string][]string // event_ticker -> [home, away]
	fired     map[string]bool     // event_ticker -> fired
	series    map[string]string   // event_ticker -> series_ticker
	emitter   OrderEmitter
	db        *store.DB // nil in backtest mode
	log       *slog.Logger
	cfg       TiebreakConfig
	replayNow *time.Time
}

func NewTiebreakStrategy(emitter OrderEmitter, log *slog.Logger, cfg TiebreakConfig) *TiebreakStrategy {
	return &TiebreakStrategy{
		prices:    make(map[string]float64),
		maxPrices: make(map[string]float64),
		markets:   make(map[string][]string),
		fired:     make(map[string]bool),
		series:    make(map[string]string),
		emitter:   emitter,
		log:       log,
		cfg:       cfg,
	}
}

// NewTiebreakStrategyWithDB creates a live-mode tiebreak that auto-loads
// series_ticker from the markets table on RegisterMarkets.
func NewTiebreakStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg TiebreakConfig) *TiebreakStrategy {
	s := NewTiebreakStrategy(emitter, log, cfg)
	s.db = db
	return s
}

// SetSeriesTicker maps event_ticker to series_ticker for series filtering.
// Implements SeriesSetter — called by backtest engine or live wiring.
func (s *TiebreakStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

func (s *TiebreakStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()

	if s.db != nil {
		s.loadSeriesTicker(eventTicker)
	}
}

func (s *TiebreakStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
}

func (s *TiebreakStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.maxPrices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *TiebreakStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *TiebreakStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
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

	// Series filter
	if !seriesMatches(s.series[eventTicker], s.cfg.SeriesFilter) {
		s.mu.Unlock()
		return
	}

	// UTC hour filter
	if !utcHourMatches(ts, s.cfg.UTCHourStart, s.cfg.UTCHourEnd) {
		s.mu.Unlock()
		return
	}

	dropPercent := (maxPrice - price) / maxPrice
	if dropPercent < s.cfg.MinDropPercent || dropPercent > s.cfg.MaxDropPercent {
		s.mu.Unlock()
		return
	}

	if price < s.cfg.MinEntryPrice || price > s.cfg.MaxEntryPrice {
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
		Context:       fmt.Sprintf("tiebreak_drop_%.0f%%", dropPercent*100),
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
		s.log.Warn("tiebreak: order dropped", "match", eventTicker, "market", marketTicker)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("tiebreak: order emitted",
		"match", eventTicker, "market", marketTicker,
		"price", price, "max_price", maxPrice,
		"drop_pct", fmt.Sprintf("%.1f%%", dropPercent*100),
		"edge_cents", edgeCents)
}

// OnPoint fires during tiebreak when a mini-break occurs (scorer != server).
// Buys the mini-broken player's market, betting on reversion (~52% returned).
func (s *TiebreakStrategy) OnPoint(eventTicker string, p store.Point) {
	if !p.IsTiebreak {
		return
	}
	// Mini-break: scorer != server during tiebreak
	if p.Scorer == p.Server {
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

	// Server was mini-broken. Buy the server's market.
	brokenMkt := mkts[0] // home
	if p.Server == 2 {
		brokenMkt = mkts[1]
	}

	series := s.series[eventTicker]
	s.mu.Unlock()

	// Series filter
	if !seriesMatches(series, s.cfg.SeriesFilter) {
		return
	}

	// UTC hour filter
	if !utcHourMatches(s.now(), s.cfg.UTCHourStart, s.cfg.UTCHourEnd) {
		return
	}

	maxPrice := s.maxPrices[brokenMkt]
	price := s.prices[brokenMkt]

	if maxPrice < s.cfg.MinPeakPrice {
		return
	}
	if price < s.cfg.MinEntryPrice || price > s.cfg.MaxEntryPrice {
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
		"point":       p.PointNumber,
		"server":      p.Server,
		"scorer":      p.Scorer,
		"home_points": p.HomePoints,
		"away_points": p.AwayPoints,
		"broken_mkt":  brokenMkt,
		"max_price":   maxPrice,
		"entry_price": price,
		"conv_prob":   s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  brokenMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("tiebreak_minibreak_set%d", p.SetNumber),
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
		s.log.Warn("tiebreak: order dropped", "match", eventTicker, "market", brokenMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("tiebreak: order emitted (score-based)",
		"match", eventTicker, "market", brokenMkt,
		"set", p.SetNumber, "score", p.HomePoints+"-"+p.AwayPoints,
		"price", price, "edge_cents", edgeCents)
}

func (s *TiebreakStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *TiebreakStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *TiebreakStrategy) eventForMarket(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *TiebreakStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.maxPrices, marketTicker)
	s.mu.Unlock()
}

func (s *TiebreakStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *TiebreakStrategy) GetPriceAge(marketTicker string) time.Duration {
	return time.Hour
}

func (s *TiebreakStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("TiebreakStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

// PreMatchGated prevents pre-match price movements from triggering
// the price-based tiebreak path before real score data arrives.
func (s *TiebreakStrategy) PreMatchGated() {}

func (s *TiebreakStrategy) OnTick(ctx context.Context) {}
