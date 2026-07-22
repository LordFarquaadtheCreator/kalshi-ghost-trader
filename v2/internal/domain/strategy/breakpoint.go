package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/pricing"
)

// breakPointState tracks set counts for the current match.
type breakPointState struct {
	setsHome int
	setsAway int
}

const (
	bpPServe          = 0.64
	bpMinEdgeCents    = 2
	bpMinPriceCents   = 5  // 0.05
	bpMaxPriceCents   = 60 // 0.60
	bpPriceStaleMS    = 60_000
)

// BreakPoint buys the returner's market when they have a break point
// opportunity, using the Markov model for fair-value pricing.
//
// Logic: When the returner can win the game (break point), compute the
// Markov fair value for the returner. If market price < fair value - edge,
// buy.
//
// Ported from v1 internal/algorithms/breakpoint.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents,
// prices in cents, ConvProb in bps.
type BreakPoint struct {
	model *pricing.MarkovModel
}

func NewBreakPoint() *BreakPoint {
	return &BreakPoint{model: pricing.NewMarkovModelWithProb(bpPServe)}
}

func (s *BreakPoint) Name() string { return "breakpoint" }

func (s *BreakPoint) OnEvent(ev match.Event, st *State) []match.Intent {
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

func (s *BreakPoint) onPoint(e match.PointScored, st *State) []match.Intent {
	if e.Point.IsTiebreak {
		return nil
	}

	bs := s.getOrCreateState(st)
	s.updateMatchState(bs, e.Point)

	// Only act on break points.
	if !e.Point.IsBreakPoint {
		return nil
	}

	// Returner is the player not serving.
	returner := 2
	if e.Point.Server == 2 {
		returner = 1
	}

	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	returnerMkt := mv.MarketTickers[returner-1]

	priceCents, ok := mv.Prices[returnerMkt]
	if !ok || priceCents <= 0 {
		return nil
	}

	priceTS, hasTS := mv.PriceTS[returnerMkt]
	if !hasTS || e.TS-priceTS > bpPriceStaleMS {
		return nil
	}

	// Compute Markov fair value for the returner.
	var fv float64
	if returner == 1 {
		fv = s.model.FairValue(
			bs.setsHome, bs.setsAway,
			e.Point.HomeGames, e.Point.AwayGames,
			e.Point.HomePoints, e.Point.AwayPoints,
			e.Point.Server, e.Point.IsTiebreak,
		)
	} else {
		// Away perspective = 1 - home probability.
		fv = 1.0 - s.model.FairValue(
			bs.setsHome, bs.setsAway,
			e.Point.HomeGames, e.Point.AwayGames,
			e.Point.HomePoints, e.Point.AwayPoints,
			e.Point.Server, e.Point.IsTiebreak,
		)
	}

	// Edge in cents: (fv - price) * 100. fv is 0-1, priceCents is 1-99.
	fairCents := int(fv * 100)
	edgeCents := fairCents - priceCents

	if edgeCents < bpMinEdgeCents {
		return nil
	}
	if priceCents < bpMinPriceCents {
		return nil
	}
	if priceCents > bpMaxPriceCents {
		return nil
	}

	convProbBps := int(fv * 10000)

	return []match.Intent{{
		MarketTicker: returnerMkt,
		Strategy:     "breakpoint",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  convProbBps,
		Reason:       "break_point_" + sideName(returner) + "_set" + intToStr(e.Point.SetNumber) + "_game" + intToStr(e.Point.GameNumber),
	}}
}

func (s *BreakPoint) updateMatchState(bs *breakPointState, p match.Point) {
	if p.HomeSetGames > bs.setsHome {
		bs.setsHome = p.HomeSetGames
	}
	if p.AwaySetGames > bs.setsAway {
		bs.setsAway = p.AwaySetGames
	}
}

func (s *BreakPoint) getOrCreateState(st *State) *breakPointState {
	v := st.Get("breakpoint")
	if v != nil {
		return v.(*breakPointState)
	}
	bs := &breakPointState{}
	st.Set("breakpoint", bs)
	return bs
}

func (s *BreakPoint) cleanup(st *State) {
	st.Set("breakpoint", nil)
}

func sideName(player int) string {
	if player == 1 {
		return "home"
	}
	return "away"
}
