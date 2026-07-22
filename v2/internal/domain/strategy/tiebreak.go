package strategy

import (
	"fmt"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// Tiebreak buys a player's YES after a sharp dip in a narrow band,
// betting on mini-break reversion. Tiebreaks are high-volatility,
// mean-reverting. Two trigger paths:
//
//   - Price-based: sharp dip from peak in band 0.40-0.65.
//   - Score-based: mini-break (scorer != server) during tiebreak.
//
// Ported from v1 internal/algorithms/tiebreak.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents.
type Tiebreak struct {
	// MinPeakPriceCents: peak price must exceed this (default 40).
	MinPeakPriceCents int
	// MinDropPercent: minimum drop from peak to trigger (default 10).
	MinDropPercent int
	// MaxDropPercent: maximum drop from peak (default 25).
	MaxDropPercent int
	// MinEntryPriceCents: don't buy below this (default 25).
	MinEntryPriceCents int
	// MaxEntryPriceCents: don't buy above this (default 60).
	MaxEntryPriceCents int
	// ConvProbBps: estimated reversion probability (default 5200 = 0.52).
	ConvProbBps int
}

func NewTiebreak() *Tiebreak {
	return &Tiebreak{
		MinPeakPriceCents:  40,
		MinDropPercent:     10,
		MaxDropPercent:     25,
		MinEntryPriceCents: 25,
		MaxEntryPriceCents: 60,
		ConvProbBps:        5200,
	}
}

func (s *Tiebreak) Name() string { return "tiebreak" }

// tiebreakState holds per-match mutable state.
type tiebreakState struct {
	prices    map[string]int // market_ticker -> latest price (cents)
	maxPrices map[string]int // market_ticker -> peak price (cents)
	fired     bool
}

func (s *Tiebreak) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("tiebreak", nil)
		}
	}
	return nil
}

func (s *Tiebreak) getOrCreateState(st *State) *tiebreakState {
	if v := st.Get("tiebreak"); v != nil {
		return v.(*tiebreakState)
	}
	ss := &tiebreakState{
		prices:    make(map[string]int),
		maxPrices: make(map[string]int),
	}
	st.Set("tiebreak", ss)
	return ss
}

func (s *Tiebreak) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}

	ss.prices[e.MarketTicker] = e.PriceCents
	if e.PriceCents > ss.maxPrices[e.MarketTicker] {
		ss.maxPrices[e.MarketTicker] = e.PriceCents
	}

	maxPrice := ss.maxPrices[e.MarketTicker]
	if maxPrice < s.MinPeakPriceCents {
		return nil
	}

	dropPercent := (maxPrice - e.PriceCents) * 100 / maxPrice
	if dropPercent < s.MinDropPercent || dropPercent > s.MaxDropPercent {
		return nil
	}

	if e.PriceCents < s.MinEntryPriceCents || e.PriceCents > s.MaxEntryPriceCents {
		return nil
	}

	convProbCents := s.ConvProbBps / 100
	edgeCents := convProbCents - e.PriceCents
	if edgeCents < 1 {
		return nil
	}

	ss.fired = true
	return []match.Intent{{
		MarketTicker: e.MarketTicker,
		Strategy:     "tiebreak",
		Action:       "buy",
		PriceCents:   e.PriceCents,
		ConvProbBps:  s.ConvProbBps,
		Reason:       fmt.Sprintf("tiebreak_drop_%d%%", dropPercent),
	}}
}

// onPoint fires during tiebreak when a mini-break occurs (scorer != server).
// Buys the mini-broken player's market, betting on reversion (~52% returned).
func (s *Tiebreak) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}
	if !e.Point.IsTiebreak {
		return nil
	}
	// Mini-break: scorer != server during tiebreak.
	if e.Point.Scorer == e.Point.Server {
		return nil
	}
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Server was mini-broken. Buy the server's market.
	brokenMkt := mv.MarketTickers[0]
	if e.Point.Server == 2 {
		brokenMkt = mv.MarketTickers[1]
	}

	maxPrice := ss.maxPrices[brokenMkt]
	price := ss.prices[brokenMkt]
	if maxPrice < s.MinPeakPriceCents {
		return nil
	}
	if price < s.MinEntryPriceCents || price > s.MaxEntryPriceCents {
		return nil
	}

	convProbCents := s.ConvProbBps / 100
	edgeCents := convProbCents - price
	if edgeCents < 1 {
		return nil
	}

	ss.fired = true
	return []match.Intent{{
		MarketTicker: brokenMkt,
		Strategy:     "tiebreak",
		Action:       "buy",
		PriceCents:   price,
		ConvProbBps:  s.ConvProbBps,
		Reason:       fmt.Sprintf("tiebreak_minibreak_set%d", e.Point.SetNumber),
	}}
}
