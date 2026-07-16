package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// FadeLongshotConfig controls the fade-longshot strategy.
// Data: at T-10min, favorite with price >= 50c won 100% (n=71, +10.4c edge).
// At T-10min, price >= 85c: 100% hit, +4.5c edge, Sharpe 1.01.
type FadeLongshotConfig struct {
	// WindowSeconds: how many seconds before close to enter.
	WindowSeconds int
	// MinPrice: minimum favorite price to enter (0 = no filter).
	MinPrice float64
	// MaxPrice: maximum favorite price to enter (0 = no cap).
	MaxPrice float64
	// BaseSize: base order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultFadeLongshotConfig() FadeLongshotConfig {
	return FadeLongshotConfig{
		WindowSeconds: 600,
		MinPrice:      0.50,
		MaxPrice:      0.0,
		BaseSize:      10.0,
		Label:         "fadelongshot",
	}
}

// FadeLongshotStrategy buys the favorite (higher-priced YES) at a fixed
// time before market close. Data shows favorites win 100% in sample
// with +10c edge at T-10min.
//
// This strategy needs close_ts for each event, provided via
// RegisterCloseTime. In live mode, close_ts comes from the markets table.
// In backtest, the backtest engine provides it.
type FadeLongshotStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64
	priceTimes map[string]time.Time
	markets    map[string][]string
	closeTimes map[string]int64
	fired      map[string]bool
	emitter    OrderEmitter
	log        *slog.Logger
	cfg        FadeLongshotConfig
	replayNow  *time.Time
}

func NewFadeLongshotStrategy(emitter OrderEmitter, log *slog.Logger, cfg FadeLongshotConfig) *FadeLongshotStrategy {
	return &FadeLongshotStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		closeTimes: make(map[string]int64),
		fired:      make(map[string]bool),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

func (s *FadeLongshotStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

// RegisterCloseTime sets the close timestamp for an event.
// closeTs is unix milliseconds.
func (s *FadeLongshotStrategy) RegisterCloseTime(eventTicker string, closeTs int64) {
	s.mu.Lock()
	s.closeTimes[eventTicker] = closeTs
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.closeTimes, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
	s.checkEntry(marketTicker)
}

func (s *FadeLongshotStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
	s.checkEntryAt(marketTicker, ts)
}

func (s *FadeLongshotStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *FadeLongshotStrategy) OnPoints([]store.Point) {}

func (s *FadeLongshotStrategy) checkEntry(marketTicker string) {
	s.checkEntryAt(marketTicker, s.now())
}

func (s *FadeLongshotStrategy) checkEntryAt(marketTicker string, ts time.Time) {
	s.mu.Lock()
	if s.fired[marketTicker] {
		s.mu.Unlock()
		return
	}

	eventTicker := ""
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				eventTicker = et
				break
			}
		}
	}
	if eventTicker == "" {
		s.mu.Unlock()
		return
	}

	closeTs, ok := s.closeTimes[eventTicker]
	if !ok || closeTs == 0 {
		s.mu.Unlock()
		return
	}

	entryWindow := int64(s.cfg.WindowSeconds) * 1000
	entryTs := closeTs - entryWindow
	if ts.UnixMilli() < entryTs {
		s.mu.Unlock()
		return
	}

	mkts := s.markets[eventTicker]
	if len(mkts) < 2 {
		s.mu.Unlock()
		return
	}

	price := s.prices[marketTicker]
	if price <= 0 {
		s.mu.Unlock()
		return
	}

	otherMkt := ""
	otherPrice := 0.0
	for _, m := range mkts {
		if m != marketTicker {
			otherMkt = m
			otherPrice = s.prices[m]
			break
		}
	}

	if otherPrice <= 0 {
		s.mu.Unlock()
		return
	}

	favMkt := marketTicker
	favPrice := price
	if otherPrice > price {
		favMkt = otherMkt
		favPrice = otherPrice
	}

	if favPrice < s.cfg.MinPrice {
		s.mu.Unlock()
		return
	}
	if s.cfg.MaxPrice > 0 && favPrice > s.cfg.MaxPrice {
		s.mu.Unlock()
		return
	}

	s.fired[favMkt] = true
	s.mu.Unlock()

	convProb := 0.99
	edgeCents := int((convProb-favPrice)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	size := s.cfg.BaseSize

	payload, _ := json.Marshal(map[string]any{
		"window_s":    s.cfg.WindowSeconds,
		"close_ts":    closeTs,
		"entry_ts":    ts.UnixMilli(),
		"fav_price":   favPrice,
		"other_price": otherPrice,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  favMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("fade_longshot_T-%ds", s.cfg.WindowSeconds),
		ConvProb:      convProb,
		MarketPrice:   favPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     0,
		Strategy:      "fadelongshot",
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("fadelongshot: order dropped", "match", eventTicker, "market", favMkt)
		return
	}
	s.log.Info("fadelongshot: order emitted",
		"match", eventTicker, "market", favMkt,
		"price", favPrice, "edge_cents", edgeCents, "size", size)
}

func (s *FadeLongshotStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *FadeLongshotStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *FadeLongshotStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("FadeLongshotStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}
