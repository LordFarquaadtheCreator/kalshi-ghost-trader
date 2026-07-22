package strategy

import (
	"fmt"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// VolumeRatio buys the side with higher cumulative dollar_volume at
// T-WindowSeconds before close. Tests whether heavy money predicts
// outcome better than price.
//
// Entry: at T-WindowSeconds before close, compare cumulative
// dollar_volume between the two YES markets. Buy the higher-volume
// side if its price is within [MinPrice, MaxPrice].
//
// Volumes are provided via the Volumes struct field (market_ticker ->
// dollar_volume in whole dollars), set before the loop runs. Close
// time is read from MatchView.OccurrenceTS.
//
// Ported from v1 internal/algorithms/volratio.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents.
type VolumeRatio struct {
	// WindowSeconds: seconds before close to enter (default 600).
	WindowSeconds int64
	// MinVolumeRatio: ratio of heavy-side vol to light-side vol to
	// trigger (e.g. 2.0 = heavy side has 2x the volume).
	MinVolumeRatio float64
	// MinPriceCents: minimum price to enter (default 10).
	MinPriceCents int
	// MaxPriceCents: maximum price to enter (default 90).
	MaxPriceCents int
	// Volumes: market_ticker -> latest dollar_volume (whole dollars).
	// Set externally before the loop processes events.
	Volumes map[string]float64
}

func NewVolumeRatio() *VolumeRatio {
	return &VolumeRatio{
		WindowSeconds:   600,
		MinVolumeRatio:  2.0,
		MinPriceCents:   10,
		MaxPriceCents:   90,
		Volumes:         map[string]float64{},
	}
}

func (s *VolumeRatio) Name() string { return "volratio" }

// volratioState holds per-match mutable state.
type volratioState struct {
	fired bool
}

func (s *VolumeRatio) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("volratio", nil)
		}
	}
	return nil
}

func (s *VolumeRatio) getOrCreateState(st *State) *volratioState {
	if v := st.Get("volratio"); v != nil {
		return v.(*volratioState)
	}
	ss := &volratioState{}
	st.Set("volratio", ss)
	return ss
}

func (s *VolumeRatio) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	mv := &st.MatchView
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	if mv.OccurrenceTS == 0 {
		return nil
	}

	// Entry window: T - WindowSeconds before close.
	entryWindowMs := s.WindowSeconds * 1000
	entryTs := mv.OccurrenceTS - entryWindowMs
	if e.TS < entryTs {
		return nil
	}

	marketTicker := e.MarketTicker
	price := e.PriceCents
	if price <= 0 {
		return nil
	}

	// Find the other market.
	otherMkt := ""
	for _, m := range mv.MarketTickers {
		if m != marketTicker {
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

	volA := s.Volumes[marketTicker]
	volB := s.Volumes[otherMkt]
	if volA <= 0 || volB <= 0 {
		return nil
	}

	// Determine heavy side.
	heavyMkt := marketTicker
	heavyPrice := price
	heavyVol := volA
	lightVol := volB
	if volB > volA {
		heavyMkt = otherMkt
		heavyPrice = otherPrice
		heavyVol = volB
		lightVol = volA
	}

	ratio := heavyVol / lightVol
	if ratio < s.MinVolumeRatio {
		return nil
	}

	if heavyPrice < s.MinPriceCents || heavyPrice > s.MaxPriceCents {
		return nil
	}

	// edge = (ratio - 1) * 5c, capped at 15c.
	edgeCents := int((ratio-1.0)*5.0 + 1e-9)
	if edgeCents > 15 {
		edgeCents = 15
	}
	if edgeCents < 1 {
		return nil
	}
	convProbCents := heavyPrice + edgeCents
	if convProbCents > 99 {
		convProbCents = 99
	}
	actualEdge := convProbCents - heavyPrice
	if actualEdge < 1 {
		return nil
	}

	ss.fired = true
	return []match.Intent{{
		MarketTicker: heavyMkt,
		Strategy:     "volratio",
		Action:       "buy",
		PriceCents:   heavyPrice,
		ConvProbBps:  convProbCents * 100,
		Reason:       fmt.Sprintf("volratio_%.1fx_T-%ds", ratio, s.WindowSeconds),
	}}
}
