package algorithms

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	minEdgeCents   = 1
	baseSize       = 10.0
	maxSize        = 100.0
	priceStaleTTL  = 60 * time.Second
	minMarketPrice = 0.05
)

// MatchPointStrategy tracks market prices and emits buy orders when
// edge exceeds threshold. Implements both Strategy and PriceLookup.
type MatchPointStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64   // market_ticker -> latest YES price (0-1)
	priceTimes map[string]time.Time // market_ticker -> last price update
	markets    map[string][]string  // event_ticker -> [home_ticker, away_ticker]
	emitter    OrderEmitter
	log        *slog.Logger

	// replayNow, when non-nil, overrides time.Now() for staleness checks.
	// Set by backtest to the timestamp of the tick being processed.
	replayNow *time.Time
}

// NewMatchPointStrategy creates a match-point detection strategy.
// emitter receives simulated buy orders. Use TickWriterEmitter for live
// or OrderCollector for backtest.
func NewMatchPointStrategy(emitter OrderEmitter, log *slog.Logger) *MatchPointStrategy {
	return &MatchPointStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		emitter:    emitter,
		log:        log,
	}
}

func (s *MatchPointStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

// UnregisterMarkets removes all state for a match — markets and prices
// for the associated market tickers.
func (s *MatchPointStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	s.mu.Unlock()
}

func (s *MatchPointStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

// OnPriceAt sets price with an explicit timestamp. Used by backtest
// to replay historical ticks with correct staleness checking.
func (s *MatchPointStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

// SetReplayTime sets the virtual "now" for staleness checks in backtest mode.
// Pass time.Time{} (zero) to disable replay mode and use wall clock again.
func (s *MatchPointStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

// now returns the effective current time — replay time if set, wall clock otherwise.
func (s *MatchPointStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func suggestedSize(absEdgeCents int) float64 {
	size := baseSize * float64(absEdgeCents) / float64(minEdgeCents)
	if size > maxSize {
		size = maxSize
	}
	return size
}

// DeletePrice removes a single market's price tracking state.
// Called by tracker on unsubscribe to prevent unbounded growth.
func (s *MatchPointStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *MatchPointStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

// GetPriceAge returns how long ago the price was last updated.
// Returns a large duration if no price exists (stale/missing).
func (s *MatchPointStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *MatchPointStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("MatchPointStrategy{markets=%d, prices=%d}",
		len(s.markets), len(s.prices))
}
