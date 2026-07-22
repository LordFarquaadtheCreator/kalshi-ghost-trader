package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// setdownState tracks peak prices per market and fired flag.
type setdownState struct {
	maxPrices map[string]int // market_ticker -> peak price in cents
	fired     bool
}

const (
	sdMinFavPriceCents   = 55 // 0.55 — was favourite
	sdMinEntryPriceCents = 30 // 0.30
	sdMaxEntryPriceCents = 45 // 0.45
	sdMinDropFromPeak    = 10 // 0.10
	sdConvProbBps        = 5500 // 0.55 — recovery probability
	sdMinEdgeCents       = 1
)

// SetDown buys a favourite's YES after their price drops into a depressed
// range, betting on set recovery. Markets overreact to set losses.
// Two trigger paths: price-based (price drops from favourite territory into
// entry range) and score-based (set loss detected at set boundary).
// Ported from v1 internal/algorithms/setdown.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents.
type SetDown struct{}

func NewSetDown() *SetDown { return &SetDown{} }

func (s *SetDown) Name() string { return "setdown" }

func (s *SetDown) OnEvent(ev match.Event, st *State) []match.Intent {
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

func (s *SetDown) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	ms := s.getOrCreateState(st)
	if ms.fired {
		return nil
	}

	if e.PriceCents <= 0 {
		return nil
	}

	if e.PriceCents > ms.maxPrices[e.MarketTicker] {
		ms.maxPrices[e.MarketTicker] = e.PriceCents
	}

	maxPrice := ms.maxPrices[e.MarketTicker]
	if maxPrice < sdMinFavPriceCents {
		return nil
	}

	// Price must have dropped from favourite territory into entry range.
	if e.PriceCents < sdMinEntryPriceCents || e.PriceCents > sdMaxEntryPriceCents {
		return nil
	}

	dropFromPeak := maxPrice - e.PriceCents
	if dropFromPeak < sdMinDropFromPeak {
		return nil
	}

	edgeCents := sdConvProbBps/100 - e.PriceCents
	if edgeCents < sdMinEdgeCents {
		return nil
	}

	ms.fired = true

	return []match.Intent{{
		MarketTicker: e.MarketTicker,
		Strategy:     "setdown",
		Action:       "buy",
		PriceCents:   e.PriceCents,
		ConvProbBps:  sdConvProbBps,
		Reason:       "setdown_price_drop",
	}}
}

func (s *SetDown) onPoint(e match.PointScored, st *State) []match.Intent {
	// Detect set boundary: first point of first game of set 2+.
	if e.Point.PointNumber != 1 || e.Point.GameNumber != 1 || e.Point.SetNumber < 2 {
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

	// Who lost the previous set.
	homeLostSet := e.Point.HomeSetGames < e.Point.AwaySetGames
	awayLostSet := e.Point.AwaySetGames < e.Point.HomeSetGames
	if !homeLostSet && !awayLostSet {
		return nil
	}

	targetMkt := mv.MarketTickers[0] // home
	if awayLostSet {
		targetMkt = mv.MarketTickers[1] // away
	}

	maxPrice := ms.maxPrices[targetMkt]
	if maxPrice < sdMinFavPriceCents {
		return nil
	}

	priceCents, ok := mv.Prices[targetMkt]
	if !ok || priceCents < sdMinEntryPriceCents || priceCents > sdMaxEntryPriceCents {
		return nil
	}

	edgeCents := sdConvProbBps/100 - priceCents
	if edgeCents < sdMinEdgeCents {
		return nil
	}

	ms.fired = true

	return []match.Intent{{
		MarketTicker: targetMkt,
		Strategy:     "setdown",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  sdConvProbBps,
		Reason:       "setdown_lost_set",
	}}
}

func (s *SetDown) getOrCreateState(st *State) *setdownState {
	v := st.Get("setdown")
	if v != nil {
		return v.(*setdownState)
	}
	ms := &setdownState{maxPrices: make(map[string]int)}
	st.Set("setdown", ms)
	return ms
}

func (s *SetDown) cleanup(st *State) {
	st.Set("setdown", nil)
}
