package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// NoFade buys the favorite when the underdog's YES price is very low
// (underdog NO price near 1). Ported from v1 internal/algorithms/nofade.go —
// decision logic preserved, mutexes removed, EmitOrder replaced with
// returned intents, float convProb replaced with ConvProbBps.
//
// convProb is derived from MaxNoPriceCents: if underdog YES <= 5c,
// favorite conversion >= 95c. Edge = convProb - favPrice in cents.
// Fires when edge >= 1 cent. close_ts proxied by MatchView.OccurrenceTS.

const (
	nfWindowSeconds   = 900
	nfMinFavCents     = 50
	nfMaxNoPriceCents = 5 // 0.05
	nfMinEdgeCents    = 1
)

type noFadeState struct {
	fired bool
}

// NoFade buys the favorite when the underdog is very cheap.
type NoFade struct {
	WindowSeconds   int
	MinFavCents     int
	MaxNoPriceCents int
}

func NewNoFade() *NoFade {
	return &NoFade{
		WindowSeconds:   nfWindowSeconds,
		MinFavCents:     nfMinFavCents,
		MaxNoPriceCents: nfMaxNoPriceCents,
	}
}

func (s *NoFade) Name() string { return "nofade" }

func (s *NoFade) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("nofade", nil)
		}
	}
	return nil
}

func (s *NoFade) getOrCreateState(st *State) *noFadeState {
	v := st.Get("nofade")
	if v != nil {
		return v.(*noFadeState)
	}
	ns := &noFadeState{}
	st.Set("nofade", ns)
	return ns
}

func (s *NoFade) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	ns := s.getOrCreateState(st)
	if ns.fired {
		return nil
	}
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	if mv.OccurrenceTS == 0 {
		return nil
	}
	windowMs := int64(s.WindowSeconds) * 1000
	entryTs := mv.OccurrenceTS - windowMs
	if e.TS < entryTs {
		return nil
	}

	p0, ok0 := mv.Prices[mv.MarketTickers[0]]
	p1, ok1 := mv.Prices[mv.MarketTickers[1]]
	if !ok0 || !ok1 {
		return nil
	}
	favMkt := mv.MarketTickers[0]
	favPrice := p0
	underdogPrice := p1
	if p1 > p0 {
		favMkt = mv.MarketTickers[1]
		favPrice = p1
		underdogPrice = p0
	}

	// underdog YES must be <= MaxNoPriceCents (favorite dominant)
	if underdogPrice > s.MaxNoPriceCents {
		return nil
	}
	if favPrice < s.MinFavCents {
		return nil
	}

	// convProb = 1 - MaxNoPrice (e.g. 0.95 -> 9500 bps)
	convProbBps := (100 - s.MaxNoPriceCents) * 100
	edgeCents := convProbBps/100 - favPrice
	if edgeCents < nfMinEdgeCents {
		return nil
	}

	ns.fired = true
	return []match.Intent{{
		MarketTicker: favMkt,
		Strategy:     "nofade",
		Action:       "buy",
		PriceCents:   favPrice,
		ConvProbBps:  convProbBps,
		Reason:       "nofade_T-" + intToStr(s.WindowSeconds) + "s_no<=" + intToStr(s.MaxNoPriceCents) + "c",
	}}
}
