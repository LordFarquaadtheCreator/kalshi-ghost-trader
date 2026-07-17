package algorithms

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SetPointConfig controls set-point strategy behavior.
// Derived from data exploration (explore.py):
//   - Overall set-point conversion: 91%
//   - Serving set-point conversion: 93%
//   - Returning set-point conversion: 89%
//   - Match-point conversion (serving): 97%
//   - Match-point conversion (returning): 89%
type SetPointConfig struct {
	// IncludeSetPoints: fire on non-match-point set points (set points in
	// sets that don't decide the match, e.g. set 1 when 0-0).
	IncludeSetPoints bool
	// IncludeReturning: fire when the set-point player is returning (breaking).
	// If false, only fire when serving.
	IncludeReturning bool
	// ServeConvProb: conversion probability when serving at set point.
	ServeConvProb float64
	// ReturnConvProb: conversion probability when returning at set point.
	ReturnConvProb float64
	// MaxMarketPrice: skip signals above this price (0 = no cap).
	MaxMarketPrice float64
	// MinMarketPrice: skip signals below this price.
	MinMarketPrice float64
	// MinEdgeCents: minimum edge to emit order.
	MinEdgeCents int
	// Label: strategy name for logging.
	Label string
}

// DefaultSetPointConfig fires on all set points (serving + returning).
func DefaultSetPointConfig() SetPointConfig {
	return SetPointConfig{
		IncludeSetPoints: true,
		IncludeReturning: true,
		ServeConvProb:    0.93,
		ReturnConvProb:   0.89,
		MaxMarketPrice:   0.0,
		MinMarketPrice:   0.05,
		MinEdgeCents:     1,
		Label:            "setpoint",
	}
}

// SetPointStrategy is a configurable set-point detection strategy.
// Generalizes MatchPointStrategy to fire on any set point, not just
// match-deciding ones. Data shows set points have 91% conversion
// but market prices them at 56c avg — a 33c edge.
type SetPointStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64
	priceTimes map[string]time.Time
	markets    map[string][]string
	emitter    OrderEmitter
	log        *slog.Logger
	cfg        SetPointConfig
	replayNow  *time.Time
}

func NewSetPointStrategy(emitter OrderEmitter, log *slog.Logger, cfg SetPointConfig) *SetPointStrategy {
	return &SetPointStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

func (s *SetPointStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *SetPointStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	s.mu.Unlock()
}

func (s *SetPointStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *SetPointStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *SetPointStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SetPointStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SetPointStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *SetPointStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *SetPointStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *SetPointStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SetPointStrategy{%s: markets=%d, prices=%d}",
		s.cfg.Label, len(s.markets), len(s.prices))
}
