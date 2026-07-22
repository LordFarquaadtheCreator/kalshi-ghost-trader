package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// breakBackState tracks per-market peak prices and the fired flag.
// One shot per match — no re-entry after firing.
type breakBackState struct {
	maxPrices map[string]int // market_ticker -> peak price in cents
	fired     bool
}

const (
	// bbConvProbBps: estimated win probability after break (recovery rate ~55%).
	bbConvProbBps = 5500
	// bbMinDropPercentBps: minimum price drop from peak (15% = 1500 bps).
	bbMinDropPercentBps = 1500
	bbMinPeakPriceCents = 30 // 0.30 — peak must exceed this
	bbMaxEntryPriceCents = 55 // 0.55 — don't buy above this
	bbPriceStaleMS       = 60_000
)

// BreakBack buys a player's YES after a break of serve.
// Markets overreact to breaks — broken player's YES drops sharply.
// Break-back rate ~25-30%. Buy the broken player at depressed price.
//
// Two detection paths ported from v1:
//  1. Price-based: sharp drop from peak proxies a break (PriceUpdate events).
//  2. Score-based: actual break detected from point data (PointScored events).
//
// Ported from v1 internal/algorithms/breakback.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents, prices in cents,
// ConvProb in bps.
type BreakBack struct{}

func NewBreakBack() *BreakBack { return &BreakBack{} }

func (s *BreakBack) Name() string { return "breakback" }

func (s *BreakBack) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			s.cleanup(st)
		}
	}
	return nil
}

// onPrice tracks peak prices and fires on sharp price drops (price-based path).
func (s *BreakBack) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	bs := s.getOrCreateState(st)
	if bs.fired {
		return nil
	}

	if e.PriceCents <= 0 {
		return nil
	}

	// Track peak price.
	if e.PriceCents > bs.maxPrices[e.MarketTicker] {
		bs.maxPrices[e.MarketTicker] = e.PriceCents
	}

	maxCents := bs.maxPrices[e.MarketTicker]
	if maxCents < bbMinPeakPriceCents {
		return nil
	}

	// Drop percent in bps: (max - price) * 10000 / max.
	dropBps := (maxCents - e.PriceCents) * 10000 / maxCents
	if dropBps < bbMinDropPercentBps {
		return nil
	}

	if e.PriceCents > bbMaxEntryPriceCents {
		return nil
	}

	// Edge = fair value cents - market price cents.
	fairCents := bbConvProbBps / 100
	edgeCents := fairCents - e.PriceCents
	if edgeCents < 1 {
		return nil
	}

	bs.fired = true

	return []match.Intent{{
		MarketTicker: e.MarketTicker,
		Strategy:     "breakback",
		Action:       "buy",
		PriceCents:   e.PriceCents,
		ConvProbBps:  bbConvProbBps,
		Reason:       "breakback_drop_" + intToStr(dropBps/100) + "pct",
	}}
}

// onPoint detects actual break of serve from score data (score-based path).
func (s *BreakBack) onPoint(e match.PointScored, st *State) []match.Intent {
	bs := s.getOrCreateState(st)
	if bs.fired {
		return nil
	}

	// Break: scorer != server on a break point.
	if !e.Point.IsBreakPoint || e.Point.Scorer == e.Point.Server {
		return nil
	}

	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Server was broken. Server 1=home -> broken is home -> market[0].
	// Server 2=away -> broken is away -> market[1].
	brokenMkt := mv.MarketTickers[0]
	if e.Point.Server == 2 {
		brokenMkt = mv.MarketTickers[1]
	}

	priceCents, ok := mv.Prices[brokenMkt]
	if !ok || priceCents <= 0 {
		return nil
	}
	if priceCents > bbMaxEntryPriceCents {
		return nil
	}

	priceTS, hasTS := mv.PriceTS[brokenMkt]
	if !hasTS || e.TS-priceTS > bbPriceStaleMS {
		return nil
	}

	maxCents := bs.maxPrices[brokenMkt]
	if maxCents < bbMinPeakPriceCents {
		return nil
	}

	fairCents := bbConvProbBps / 100
	edgeCents := fairCents - priceCents
	if edgeCents < 1 {
		return nil
	}

	bs.fired = true

	return []match.Intent{{
		MarketTicker: brokenMkt,
		Strategy:     "breakback",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  bbConvProbBps,
		Reason:       "breakback_set" + intToStr(e.Point.SetNumber) + "_game" + intToStr(e.Point.GameNumber),
	}}
}

func (s *BreakBack) getOrCreateState(st *State) *breakBackState {
	v := st.Get("breakback")
	if v != nil {
		return v.(*breakBackState)
	}
	bs := &breakBackState{maxPrices: make(map[string]int)}
	st.Set("breakback", bs)
	return bs
}

func (s *BreakBack) cleanup(st *State) {
	st.Set("breakback", nil)
}
