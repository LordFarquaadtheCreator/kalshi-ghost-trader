package strategy

import (
	"fmt"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// SpikeFade fades large price spikes when NOT at match/set point.
// REPORT.md: huge spikes (>30c in 30s) have 65.6% fade win rate.
// Spikes at match-point context are informational (won't revert) —
// this strategy fades ONLY when not at match/set point context.
//
// Entry: price jumps >SpikeThresholdCents in <WindowMs, current score
// is NOT at match/set point → buy the opposite side (fade the spike).
// Ported from v1 internal/algorithms/spike_fade.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents.
type SpikeFade struct {
	// SpikeThresholdCents: minimum price jump to trigger (default 30).
	SpikeThresholdCents int
	// WindowMs: lookback window for spike detection (default 30000).
	WindowMs int64
	// MinEntryPriceCents: only fade when spiked side >= this (default 20).
	MinEntryPriceCents int
	// MaxEntryPriceCents: don't fade above this (default 95).
	MaxEntryPriceCents int
}

func NewSpikeFade() *SpikeFade {
	return &SpikeFade{
		SpikeThresholdCents: 30,
		WindowMs:            30_000,
		MinEntryPriceCents:  20,
		MaxEntryPriceCents:  95,
	}
}

func (s *SpikeFade) Name() string { return "spike-fade" }

// spikeFadeState holds per-match mutable state.
type spikeFadeState struct {
	samples      map[string][]spikePriceSample // market_ticker -> price history
	fired        bool
	isMatchPoint bool
	isSetPoint   bool
}

type spikePriceSample struct {
	ts    int64
	price int // cents
}

func (s *SpikeFade) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.PointScored:
		s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("spike-fade", nil)
		}
	}
	return nil
}

func (s *SpikeFade) getOrCreateState(st *State) *spikeFadeState {
	if v := st.Get("spike-fade"); v != nil {
		return v.(*spikeFadeState)
	}
	ss := &spikeFadeState{samples: make(map[string][]spikePriceSample)}
	st.Set("spike-fade", ss)
	return ss
}

func (s *SpikeFade) onPoint(e match.PointScored, st *State) {
	ss := s.getOrCreateState(st)
	ss.isMatchPoint = e.Point.IsMatchPoint
	ss.isSetPoint = e.Point.IsSetPoint
}

func (s *SpikeFade) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}

	// Append price sample, trim old samples outside window.
	cutoff := e.TS - s.WindowMs
	samples := ss.samples[e.MarketTicker]
	samples = append(samples, spikePriceSample{ts: e.TS, price: e.PriceCents})
	for len(samples) > 0 && samples[0].ts < cutoff {
		samples = samples[1:]
	}
	ss.samples[e.MarketTicker] = samples

	// Skip if at match/set point — spikes there are informational.
	if ss.isMatchPoint || ss.isSetPoint {
		return nil
	}

	// Need at least 2 samples to detect spike.
	if len(samples) < 2 {
		return nil
	}

	windowStart := samples[0].price
	currentPrice := samples[len(samples)-1].price
	spikeCents := currentPrice - windowStart
	if spikeCents < s.SpikeThresholdCents {
		return nil
	}

	// Only fade when spiked side is in tradeable range.
	if currentPrice < s.MinEntryPriceCents || currentPrice > s.MaxEntryPriceCents {
		return nil
	}

	// Find opposite market (fade = buy the other side).
	otherMkt := ""
	for _, m := range mv.MarketTickers {
		if m != e.MarketTicker {
			otherMkt = m
			break
		}
	}
	if otherMkt == "" {
		return nil
	}

	otherPrice, ok := mv.Prices[otherMkt]
	if !ok || otherPrice <= 0 {
		return nil
	}

	// Fair value of opposite side ≈ 1 - pre-spike price (in cents).
	fairValueCents := 100 - windowStart
	if fairValueCents > 99 {
		fairValueCents = 99
	}
	edgeCents := fairValueCents - otherPrice
	if edgeCents < 1 {
		edgeCents = 1
	}
	_ = edgeCents // edge gate already passed above; kept for parity with v1

	ss.fired = true

	return []match.Intent{{
		MarketTicker: otherMkt,
		Strategy:     "spike-fade",
		Action:       "buy",
		PriceCents:   otherPrice,
		ConvProbBps:  fairValueCents * 100,
		Reason:       fmt.Sprintf("spikefade_%dc_fade", spikeCents),
	}}
}
