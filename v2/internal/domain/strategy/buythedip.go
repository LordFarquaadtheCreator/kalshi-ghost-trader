package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// buythedip buys markets that dip sharply, then sells on recovery (TP),
// further drop (SL), or time exit.
//
// Ported from v1 internal/algorithms/buythedip.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents, prices in cents.
// Sliding window and position tracking stored in per-strategy state.

const (
	btdDipThresholdCents = 30
	btdWindowMS          = 30_000
	btdMinEntryCents     = 15
	btdMaxEntryCents     = 80
	btdMinPreDipCents    = 50
	btdTPFracNum         = 3 // 3/4 = 0.75 recovery
	btdTPFracDen         = 4
	btdStopLossCents     = 10
	btdMaxHoldMS         = 300_000
)

type btdPriceSample struct {
	ts    int64 // unix ms
	price int   // cents
}

type btdPosition struct {
	entryPrice int   // cents
	dipSize    int   // cents (abs drop that triggered entry)
	entryTS    int64 // unix ms
}

type btdState struct {
	samples   map[string][]btdPriceSample
	positions map[string]*btdPosition
}

// BuyTheDip detects sharp price drops and buys for recovery.
type BuyTheDip struct{}

func NewBuyTheDip() *BuyTheDip { return &BuyTheDip{} }

func (s *BuyTheDip) Name() string { return "buythedip" }

func (s *BuyTheDip) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			s.cleanup(st)
		}
	}
	return nil
}

func (s *BuyTheDip) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	mv := &st.MatchView
	bs := s.getOrCreateState(st)

	// Update sliding window, trim old samples.
	cutoff := e.TS - btdWindowMS
	samples := bs.samples[e.MarketTicker]
	samples = append(samples, btdPriceSample{ts: e.TS, price: e.PriceCents})
	for len(samples) > 0 && samples[0].ts < cutoff {
		samples = samples[1:]
	}
	bs.samples[e.MarketTicker] = samples

	// Check exit first if position open.
	if pos, ok := bs.positions[e.MarketTicker]; ok {
		return s.checkExit(e, pos, bs)
	}

	// No open position — check for dip entry.
	return s.checkEntry(e, mv, samples, bs)
}

// checkEntry detects a dip and returns a buy intent. Dips at match/set point
// are excluded — those are informational and don't revert.
func (s *BuyTheDip) checkEntry(e match.PriceUpdate, mv *MatchView, samples []btdPriceSample, bs *btdState) []match.Intent {
	if len(samples) < 2 {
		return nil
	}
	if _, ok := bs.positions[e.MarketTicker]; ok {
		return nil
	}

	// Exclude match/set point context.
	if mv.IsMatchPoint || mv.IsSetPoint {
		return nil
	}

	windowStart := samples[0].price
	dipCents := windowStart - e.PriceCents
	if dipCents < btdDipThresholdCents {
		return nil
	}

	// Pre-dip price must exceed min — favourite proxy (winner market).
	if windowStart < btdMinPreDipCents {
		return nil
	}

	// Entry price must be in tradeable range.
	if e.PriceCents < btdMinEntryCents || e.PriceCents > btdMaxEntryCents {
		return nil
	}

	// ConvProb: pre-dip price as fair value proxy, capped at 99c.
	convProbCents := windowStart
	if convProbCents > 99 {
		convProbCents = 99
	}
	edgeCents := convProbCents - e.PriceCents
	if edgeCents < 1 {
		edgeCents = 1
	}
	_ = edgeCents

	// Record position before returning intent to avoid re-entry on next tick.
	bs.positions[e.MarketTicker] = &btdPosition{
		entryPrice: e.PriceCents,
		dipSize:    dipCents,
		entryTS:    e.TS,
	}

	return []match.Intent{{
		MarketTicker: e.MarketTicker,
		Strategy:     "buythedip",
		Action:       "buy",
		PriceCents:   e.PriceCents,
		ConvProbBps:  convProbCents * 100,
		Reason:       "btd_dip_" + intToStr(dipCents) + "c",
	}}
}

// checkExit evaluates TP/SL/time exit for an open position.
func (s *BuyTheDip) checkExit(e match.PriceUpdate, pos *btdPosition, bs *btdState) []match.Intent {
	tpPrice := pos.entryPrice + (pos.dipSize*btdTPFracNum)/btdTPFracDen
	slPrice := pos.entryPrice - btdStopLossCents

	reason := ""
	sellPrice := 0

	switch {
	case e.PriceCents >= tpPrice:
		reason = "tp"
		sellPrice = tpPrice
	case e.PriceCents <= slPrice:
		reason = "sl"
		sellPrice = slPrice
	case e.TS-pos.entryTS >= btdMaxHoldMS:
		reason = "time"
		sellPrice = e.PriceCents
	}

	if reason == "" {
		return nil
	}

	// Clear position before returning intent.
	delete(bs.positions, e.MarketTicker)

	return []match.Intent{{
		MarketTicker: e.MarketTicker,
		Strategy:     "buythedip",
		Action:       "sell",
		PriceCents:   sellPrice,
		ConvProbBps:  pos.entryPrice * 100,
		Reason:       "btd_sell_" + reason,
	}}
}

func (s *BuyTheDip) getOrCreateState(st *State) *btdState {
	v := st.Get("buythedip")
	if v != nil {
		return v.(*btdState)
	}
	bs := &btdState{
		samples:   make(map[string][]btdPriceSample),
		positions: make(map[string]*btdPosition),
	}
	st.Set("buythedip", bs)
	return bs
}

func (s *BuyTheDip) cleanup(st *State) {
	st.Set("buythedip", nil)
}
