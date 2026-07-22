package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// adOutState tracks the open ad-out position for the current match.
// One position at a time — no stacking.
type adOutState struct {
	open          bool
	marketTicker  string
	buyPriceCents int
	buyPointKey   string // set:game:point of the triggering point
	buyTS         int64
}

const (
	// adOutConvProbBps: empirical returner win rate from ad-out (RQ7: 82%).
	adOutConvProbBps = 8200
	adOutMinPriceCents = 5  // 0.05 liquidity floor
	adOutMaxPriceCents = 85 // 0.85 edge ceiling
	adOutMinEdgeCents  = 3
	adOutPriceStaleMS  = 60_000
)

// AdOut exploits the apitennis latency edge on ad-out points.
// Ad-out = returner has Advantage (can win game with next point = break).
// Buy returner YES before kalshi prices react; sell on the next point
// event when price has caught up.
//
// Ported from v1 internal/algorithms/adout.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents, prices in
// cents, ConvProb in bps.
type AdOut struct{}

func NewAdOut() *AdOut { return &AdOut{} }

func (s *AdOut) Name() string { return "adout" }

func (s *AdOut) OnEvent(ev match.Event, st *State) []match.Intent {
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

func (s *AdOut) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	as := s.getOrCreateState(st)

	// First: sell any open position on the next point (not the buy point).
	if sell := s.maybeSell(as, e, mv); sell != nil {
		return sell
	}

	// Then: check if this point is ad-out and buy if so.
	return s.maybeBuy(as, e, mv)
}

// maybeSell emits a sell intent for an open position on the next point.
// Returns nil if no open position or same point as the buy.
func (s *AdOut) maybeSell(as *adOutState, e match.PointScored, mv *MatchView) []match.Intent {
	if !as.open {
		return nil
	}
	pk := pointKeyStr(e.Point.SetNumber, e.Point.GameNumber, e.Point.PointNumber)
	if pk == as.buyPointKey {
		return nil // don't sell on the same point that triggered the buy
	}

	priceCents, ok := mv.Prices[as.marketTicker]
	if !ok || priceCents <= 0 {
		as.open = false
		return nil
	}

	priceTS, hasTS := mv.PriceTS[as.marketTicker]
	if !hasTS || e.TS-priceTS > adOutPriceStaleMS {
		as.open = false
		return nil
	}

	// Clear position before emitting to avoid re-entry.
	as.open = false

	return []match.Intent{{
		MarketTicker: as.marketTicker,
		Strategy:     "adout",
		Action:       "sell",
		PriceCents:   priceCents,
		ConvProbBps:  adOutConvProbBps,
		Reason:       "adout_sell_set" + intToStr(e.Point.SetNumber) + "_game" + intToStr(e.Point.GameNumber),
	}}
}

// maybeBuy detects ad-out and buys returner YES.
// Ad-out: returner has "A" (advantage).
//   server=1 (home serving) && away_points=="A" → returner is away (player 2)
//   server=2 (away serving) && home_points=="A" → returner is home (player 1)
func (s *AdOut) maybeBuy(as *adOutState, e match.PointScored, mv *MatchView) []match.Intent {
	if e.Point.IsTiebreak {
		return nil // tiebreak ad logic differs — no deuce/advantage cycle
	}
	if as.open {
		return nil // one position per match — no stacking
	}

	returner := 0
	if e.Point.Server == 1 && e.Point.AwayPoints == "A" {
		returner = 2
	} else if e.Point.Server == 2 && e.Point.HomePoints == "A" {
		returner = 1
	} else {
		return nil // not ad-out
	}

	if len(mv.MarketTickers) < 2 {
		return nil
	}
	returnerMkt := mv.MarketTickers[returner-1]

	priceCents, ok := mv.Prices[returnerMkt]
	if !ok || priceCents <= 0 {
		return nil
	}

	priceTS, hasTS := mv.PriceTS[returnerMkt]
	if !hasTS || e.TS-priceTS > adOutPriceStaleMS {
		return nil
	}

	// Edge = fair value cents - market price cents.
	// Fair value = 82 cents (8200 bps / 100).
	fairCents := adOutConvProbBps / 100
	edgeCents := fairCents - priceCents
	if edgeCents < adOutMinEdgeCents {
		return nil
	}
	if priceCents < adOutMinPriceCents {
		return nil
	}
	if priceCents > adOutMaxPriceCents {
		return nil
	}

	// Record open position.
	as.open = true
	as.marketTicker = returnerMkt
	as.buyPriceCents = priceCents
	as.buyPointKey = pointKeyStr(e.Point.SetNumber, e.Point.GameNumber, e.Point.PointNumber)
	as.buyTS = e.TS

	return []match.Intent{{
		MarketTicker: returnerMkt,
		Strategy:     "adout",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  adOutConvProbBps,
		Reason:       "adout_buy_set" + intToStr(e.Point.SetNumber) + "_game" + intToStr(e.Point.GameNumber) + "_pt" + intToStr(e.Point.PointNumber),
	}}
}

func (s *AdOut) getOrCreateState(st *State) *adOutState {
	v := st.Get("adout")
	if v != nil {
		return v.(*adOutState)
	}
	as := &adOutState{}
	st.Set("adout", as)
	return as
}

func (s *AdOut) cleanup(st *State) {
	st.Set("adout", nil)
}
