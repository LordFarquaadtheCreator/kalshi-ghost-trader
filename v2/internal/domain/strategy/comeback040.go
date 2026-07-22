package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// Comeback040 buys the server's YES when they're down 0-40 on serve.
// Market over-reacts to 0-40, pricing server as nearly certain to be broken.
// Match-win probability is higher than depressed price implies — even if
// broken, server can break back and win.
//
// Ported from v1 internal/algorithms/comeback040.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents, prices in cents,
// ConvProb in basis points. Peak price tracking via PriceUpdate events.

const (
	cbMinPeakCents  = 40
	cbMaxEntryCents = 70
	cbMinEntryCents = 10
	cbConvProbBps   = 3500 // 0.35
)

type comebackState struct {
	maxPrices map[string]int
	fired     bool
}

// Comeback040 detects 0-40 on serve and buys the server's market.
type Comeback040 struct{}

func NewComeback040() *Comeback040 { return &Comeback040{} }

func (s *Comeback040) Name() string { return "comeback040" }

func (s *Comeback040) OnEvent(ev match.Event, st *State) []match.Intent {
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

func (s *Comeback040) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	cs := s.getOrCreateState(st)
	if e.PriceCents > cs.maxPrices[e.MarketTicker] {
		cs.maxPrices[e.MarketTicker] = e.PriceCents
	}
	return nil
}

// onPoint detects 0-40 on serve and fires on the server's market.
func (s *Comeback040) onPoint(e match.PointScored, st *State) []match.Intent {
	cs := s.getOrCreateState(st)
	if cs.fired {
		return nil
	}

	mv := &st.MatchView
	p := e.Point

	// 0-40 on serve: server down 0-40 from server's perspective.
	is040 := false
	if p.HomePoints == "0" && p.AwayPoints == "40" && p.Server == 1 {
		// Home serving, down 0-40.
		is040 = true
	} else if p.HomePoints == "40" && p.AwayPoints == "0" && p.Server == 2 {
		// Away serving, down 0-40.
		is040 = true
	}
	if !is040 {
		return nil
	}

	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Server 1=home → market[0], Server 2=away → market[1].
	serverMkt := mv.MarketTickers[0]
	if p.Server == 2 {
		serverMkt = mv.MarketTickers[1]
	}

	price, ok := mv.Prices[serverMkt]
	if !ok || price <= 0 {
		return nil
	}

	maxPrice := cs.maxPrices[serverMkt]
	if maxPrice < cbMinPeakCents {
		return nil
	}
	if price > cbMaxEntryCents {
		return nil
	}
	if price < cbMinEntryCents {
		return nil
	}

	convProbCents := cbConvProbBps / 100 // 35
	edgeCents := convProbCents - price
	if edgeCents < 1 {
		return nil
	}

	cs.fired = true
	return []match.Intent{{
		MarketTicker: serverMkt,
		Strategy:     "comeback040",
		Action:       "buy",
		PriceCents:   price,
		ConvProbBps:  cbConvProbBps,
		Reason:       "comeback040_set" + intToStr(p.SetNumber) + "_game" + intToStr(p.GameNumber),
	}}
}

func (s *Comeback040) getOrCreateState(st *State) *comebackState {
	v := st.Get("comeback040")
	if v != nil {
		return v.(*comebackState)
	}
	cs := &comebackState{
		maxPrices: make(map[string]int),
	}
	st.Set("comeback040", cs)
	return cs
}

func (s *Comeback040) cleanup(st *State) {
	st.Set("comeback040", nil)
}
