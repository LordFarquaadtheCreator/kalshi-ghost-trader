package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// set1winnerState tracks whether the strategy already fired for this match.
type set1winnerState struct {
	fired bool
}

const (
	s1wConvProbBps  = 7200 // 0.72 — set 1 winner wins match 72%
	s1wMaxPriceCents = 72  // 0.72
	s1wMinEdgeCents  = 5
)

// Set1Winner buys the set 1 winner's YES at the start of set 2.
// Empirical: set 1 winner wins match 72%. If market prices that below 72c,
// there is an edge. Ported from v1 internal/algorithms/set1winner.go —
// decision logic preserved, mutexes removed, EmitOrder replaced with intents.
type Set1Winner struct{}

func NewSet1Winner() *Set1Winner { return &Set1Winner{} }

func (s *Set1Winner) Name() string { return "set1winner" }

func (s *Set1Winner) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			s.cleanup(st)
		}
	}
	return nil
}

func (s *Set1Winner) onPoint(e match.PointScored, st *State) []match.Intent {
	// Fire on first point of set 2 (set 1 just ended).
	if e.Point.SetNumber != 2 || e.Point.PointNumber != 1 {
		return nil
	}

	ms := s.getOrCreateState(st)
	if ms.fired {
		return nil
	}

	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Set 1 winner from set games at start of set 2.
	homeWonSet1 := e.Point.HomeSetGames > e.Point.AwaySetGames
	winnerMkt := mv.MarketTickers[0]
	if !homeWonSet1 {
		winnerMkt = mv.MarketTickers[1]
	}

	priceCents, ok := mv.Prices[winnerMkt]
	if !ok || priceCents <= 0 || priceCents > s1wMaxPriceCents {
		return nil
	}

	edgeCents := s1wMaxPriceCents - priceCents
	if edgeCents < s1wMinEdgeCents {
		return nil
	}

	ms.fired = true

	return []match.Intent{{
		MarketTicker: winnerMkt,
		Strategy:     "set1winner",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  s1wConvProbBps,
		Reason:       "set1winner_s2start",
	}}
}

func (s *Set1Winner) getOrCreateState(st *State) *set1winnerState {
	v := st.Get("set1winner")
	if v != nil {
		return v.(*set1winnerState)
	}
	ms := &set1winnerState{}
	st.Set("set1winner", ms)
	return ms
}

func (s *Set1Winner) cleanup(st *State) {
	st.Set("set1winner", nil)
}
