package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// SpikeFadeConfig controls the spike-fade strategy.
// REPORT.md: huge spikes (>30c in 30s) have 65.6% fade win rate,
// +5.75c avg, Sharpe 0.44. But spikes at match-point context are
// informational (won't revert). This strategy fades huge spikes
// ONLY when not at match-point / set-point context.
//
// Entry: when price jumps >SpikeThreshold cents in <WindowSeconds,
// and current score is NOT at match/set point, sell the spike
// (buy the opposite side). Hold HoldSeconds, exit at market price.
type SpikeFadeConfig struct {
	// SpikeThresholdCents: minimum price jump in cents to trigger (default 30)
	SpikeThresholdCents int
	// WindowSeconds: lookback window for spike detection (default 30)
	WindowSeconds int
	// HoldSeconds: how long to hold before exit (default 60)
	HoldSeconds int
	// MinEntryPrice: only fade when spiked side is above this (default 0.20)
	// Don't fade cheap contracts — limited downside.
	MinEntryPrice float64
	// MaxEntryPrice: don't fade above this (default 0.95)
	MaxEntryPrice float64
	// BaseSize: order size in dollars
	BaseSize float64
	// Label: strategy label
	Label string
}

func DefaultSpikeFadeConfig() SpikeFadeConfig {
	return SpikeFadeConfig{
		SpikeThresholdCents: 30,
		WindowSeconds:       30,
		HoldSeconds:         60,
		MinEntryPrice:       0.20,
		MaxEntryPrice:       0.95,
		BaseSize:            10.0,
		Label:               "spike-fade",
	}
}

// priceSample is a timestamped price for spike detection.
type priceSample struct {
	ts    int64
	price float64
}

// SpikeFadeStrategy fades large price spikes when NOT at match/set point.
// Tracks price history per market, detects spikes, checks score context,
// then buys the opposite side (fade the spike).
//
// Implements ScoreObserver to track match/set point context.
// Implements PreMatchGated — only fires after match starts.
type SpikeFadeStrategy struct {
	mu          sync.RWMutex
	prices      map[string][]priceSample
	markets     map[string][]string
	fired       map[string]bool // per event
	scoreState  map[string]*spikeScoreState
	emitter     OrderEmitter
	log         *slog.Logger
	cfg         SpikeFadeConfig
	replayNow   *time.Time
}

type spikeScoreState struct {
	isMatchPoint bool
	isSetPoint   bool
}

func NewSpikeFadeStrategy(emitter OrderEmitter, log *slog.Logger, cfg SpikeFadeConfig) *SpikeFadeStrategy {
	return &SpikeFadeStrategy{
		prices:     make(map[string][]priceSample),
		markets:    make(map[string][]string),
		fired:      make(map[string]bool),
		scoreState: make(map[string]*spikeScoreState),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

func (s *SpikeFadeStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *SpikeFadeStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	mkts := s.markets[eventTicker]
	for _, mkt := range mkts {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.scoreState, eventTicker)
	s.mu.Unlock()
}

func (s *SpikeFadeStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *SpikeFadeStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	// Append price sample, trim old samples outside window
	windowMs := int64(s.cfg.WindowSeconds) * 1000
	cutoff := ts.UnixMilli() - windowMs
	samples := s.prices[marketTicker]
	samples = append(samples, priceSample{ts: ts.UnixMilli(), price: price})
	// Trim
	for len(samples) > 0 && samples[0].ts < cutoff {
		samples = samples[1:]
	}
	s.prices[marketTicker] = samples

	// Find event for this market
	eventTicker := ""
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				eventTicker = et
				break
			}
		}
	}
	if eventTicker == "" || s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	// Check score context — skip if at match/set point
	ss := s.scoreState[eventTicker]
	if ss != nil && (ss.isMatchPoint || ss.isSetPoint) {
		s.mu.Unlock()
		return
	}

	// Need at least 2 samples to detect spike
	if len(samples) < 2 {
		s.mu.Unlock()
		return
	}

	// Detect spike: price jumped >threshold from window start to now
	windowStart := samples[0].price
	currentPrice := samples[len(samples)-1].price
	spikeCents := int((currentPrice - windowStart) * 100)

	if spikeCents < s.cfg.SpikeThresholdCents {
		s.mu.Unlock()
		return
	}

	// Only fade when spiked side is in tradeable range
	if currentPrice < s.cfg.MinEntryPrice || currentPrice > s.cfg.MaxEntryPrice {
		s.mu.Unlock()
		return
	}

	// Find opposite market (fade = buy the other side)
	mkts := s.markets[eventTicker]
	otherMkt := ""
	for _, m := range mkts {
		if m != marketTicker {
			otherMkt = m
			break
		}
	}
	if otherMkt == "" {
		s.mu.Unlock()
		return
	}

	otherSamples := s.prices[otherMkt]
	otherPrice := 0.0
	if len(otherSamples) > 0 {
		otherPrice = otherSamples[len(otherSamples)-1].price
	}
	if otherPrice <= 0 {
		s.mu.Unlock()
		return
	}

	s.fired[eventTicker] = true
	s.mu.Unlock()

	// Fade: buy the opposite side (price dropped as spike side rose)
	// convProb: if spike reverts, opposite side recovers.
	// Estimate: opposite side fair value ≈ 1 - windowStart (pre-spike)
	fairValue := 1.0 - windowStart
	if fairValue > 0.99 {
		fairValue = 0.99
	}
	edgeCents := int((fairValue-otherPrice)*100 + 1e-9)
	if edgeCents < 1 {
		edgeCents = 1
	}

	payload, _ := json.Marshal(map[string]any{
		"spiked_mkt":     marketTicker,
		"spiked_price":   currentPrice,
		"pre_spike":      windowStart,
		"spike_cents":    spikeCents,
		"fade_mkt":       otherMkt,
		"fade_price":     otherPrice,
		"fair_value":     fairValue,
		"hold_seconds":   s.cfg.HoldSeconds,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  otherMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("spikefade_%dc_fade", spikeCents),
		ConvProb:      fairValue,
		MarketPrice:   otherPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: s.cfg.BaseSize,
		SetNumber:     0,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("spike-fade: order dropped", "match", eventTicker, "market", otherMkt)
		return
	}
	s.log.Info("spike-fade: order emitted",
		"match", eventTicker, "market", otherMkt,
		"spiked_mkt", marketTicker, "spike_cents", spikeCents,
		"fade_price", otherPrice, "edge_cents", edgeCents)
}

func (s *SpikeFadeStrategy) OnPoint(eventTicker string, p store.Point) {
	s.mu.Lock()
	ss, ok := s.scoreState[eventTicker]
	if !ok {
		ss = &spikeScoreState{}
		s.scoreState[eventTicker] = ss
	}
	ss.isMatchPoint = p.IsMatchPoint
	ss.isSetPoint = p.IsSetPoint
	s.mu.Unlock()
}

func (s *SpikeFadeStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SpikeFadeStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SpikeFadeStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *SpikeFadeStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SpikeFadeStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

func (s *SpikeFadeStrategy) PreMatchGated() {}
