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

// NoFadeConfig controls the no-fade underdog strategy.
// Buys the favorite when the underdog's NO price is very low (< MaxNoPrice),
// using NO price = 1 - YES price for the underdog market.
// This captures edge from rounding/liquidity gaps in the NO market.
type NoFadeConfig struct {
	// WindowSeconds: how many seconds before close to enter.
	WindowSeconds int
	// MinFavPrice: minimum favorite YES price to enter.
	MinFavPrice float64
	// MaxNoPrice: maximum underdog NO price to enter (e.g. 0.05 = underdog YES <= 0.05).
	MaxNoPrice float64
	// BaseSize: base order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultNoFadeConfig() NoFadeConfig {
	return NoFadeConfig{
		WindowSeconds: 900,
		MinFavPrice:   0.50,
		MaxNoPrice:    0.05,
		BaseSize:      10.0,
		Label:         "nofade",
	}
}

// NoFadeStrategy buys the favorite when the underdog's NO price is very low.
// NO price is derived as 1 - YES price. When underdog YES <= 0.05,
// the favorite is near-certain but market rounding may leave edge.
//
// Uses convProb = 1 - maxNoPrice (e.g. 0.95) so edge = (0.95 - favPrice) * 100.
// At favPrice 0.93, edge = 2c. At favPrice 0.94, edge = 1c.
// Fires when edge >= 1 cent.
type NoFadeStrategy struct {
	mu          sync.RWMutex
	prices      map[string]float64
	priceTimes  map[string]time.Time
	markets     map[string][]string
	closeTimes  map[string]int64
	fired       map[string]bool
	closeWarned map[string]bool
	emitter     OrderEmitter
	db          *store.DB
	log         *slog.Logger
	cfg         NoFadeConfig
	replayNow   *time.Time
}

func NewNoFadeStrategy(emitter OrderEmitter, log *slog.Logger, cfg NoFadeConfig) *NoFadeStrategy {
	return &NoFadeStrategy{
		prices:      make(map[string]float64),
		priceTimes:  make(map[string]time.Time),
		markets:     make(map[string][]string),
		closeTimes:  make(map[string]int64),
		fired:       make(map[string]bool),
		closeWarned: make(map[string]bool),
		emitter:     emitter,
		log:         log,
		cfg:         cfg,
	}
}

func NewNoFadeStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg NoFadeConfig) *NoFadeStrategy {
	s := NewNoFadeStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *NoFadeStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()

	if s.db != nil {
		s.loadCloseTime(eventTicker)
	}
}

func (s *NoFadeStrategy) loadCloseTime(eventTicker string) {
	mkts, err := s.db.GetMarketsByEvent(context.Background(), eventTicker)
	if err != nil {
		s.log.Error("nofade: load close_ts", "event", eventTicker, "err", err)
		return
	}
	for _, m := range mkts {
		if m.CloseTS > 0 {
			s.mu.Lock()
			s.closeTimes[eventTicker] = m.CloseTS
			delete(s.closeWarned, eventTicker)
			s.mu.Unlock()
			s.log.Info("nofade: loaded close_ts", "event", eventTicker, "close_ts", m.CloseTS)
			return
		}
	}
	s.mu.Lock()
	if !s.closeWarned[eventTicker] {
		s.closeWarned[eventTicker] = true
		s.mu.Unlock()
		s.log.Warn("nofade: no close_ts for event", "event", eventTicker)
		return
	}
	s.mu.Unlock()
}

func (s *NoFadeStrategy) RegisterCloseTime(eventTicker string, closeTs int64) {
	s.mu.Lock()
	s.closeTimes[eventTicker] = closeTs
	s.mu.Unlock()
}

func (s *NoFadeStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.closeTimes, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.closeWarned, eventTicker)
	s.mu.Unlock()
}

func (s *NoFadeStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
	s.checkEntry(marketTicker)
}

func (s *NoFadeStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
	s.checkEntryAt(marketTicker, ts)
}

func (s *NoFadeStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *NoFadeStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *NoFadeStrategy) checkEntry(marketTicker string) {
	s.checkEntryAt(marketTicker, s.now())
}

func (s *NoFadeStrategy) checkEntryAt(marketTicker string, ts time.Time) {
	s.mu.Lock()

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

	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	closeTs, ok := s.closeTimes[eventTicker]
	if !ok || closeTs == 0 {
		s.mu.Unlock()
		if s.db != nil {
			s.loadCloseTime(eventTicker)
		}
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

	// Identify favorite (higher YES) and underdog (lower YES)
	favMkt := marketTicker
	favPrice := price
	underdogPrice := otherPrice
	if otherPrice > price {
		favMkt = otherMkt
		favPrice = otherPrice
		underdogPrice = price
	}

	// Underdog YES must be <= MaxNoPrice (underdog very cheap = favorite dominant)
	if underdogPrice > s.cfg.MaxNoPrice {
		s.mu.Unlock()
		return
	}

	// Favorite must meet min price
	if favPrice < s.cfg.MinFavPrice {
		s.mu.Unlock()
		return
	}

	s.fired[eventTicker] = true
	s.mu.Unlock()

	convProb := 0.99
	edgeCents := int((convProb-favPrice)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	size := s.cfg.BaseSize

	payload, _ := json.Marshal(map[string]any{
		"window_s":     s.cfg.WindowSeconds,
		"close_ts":     closeTs,
		"entry_ts":     ts.UnixMilli(),
		"fav_price":    favPrice,
		"underdog_yes": underdogPrice,
		"underdog_no":  1.0 - underdogPrice,
		"max_no_price": s.cfg.MaxNoPrice,
		"conv_prob":    convProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  favMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("nofade_T-%ds_no<%.2f", s.cfg.WindowSeconds, s.cfg.MaxNoPrice),
		ConvProb:      convProb,
		MarketPrice:   favPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     0,
		Strategy:      "nofade",
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("nofade: order dropped", "match", eventTicker, "market", favMkt)
		return
	}
	s.log.Info("nofade: order emitted",
		"match", eventTicker, "market", favMkt,
		"price", favPrice, "underdog_yes", underdogPrice,
		"edge_cents", edgeCents, "size", size)
}

func (s *NoFadeStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *NoFadeStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *NoFadeStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *NoFadeStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("NoFadeStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}
