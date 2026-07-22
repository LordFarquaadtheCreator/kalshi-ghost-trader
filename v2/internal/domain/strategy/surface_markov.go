package strategy

import (
	"fmt"
	"math"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/pricing"
)

// SurfaceMarkov uses surface + series calibrated pServe in the Markov
// model. Trades when market diverges from calibrated fair value.
//
// Replaces global pServe=0.64 with surface + series specific serve rates.
// Empirical hold rates from 4 days of data:
//
//	ATp main: 61.3%   WTa main: 52.0%
//	ATp challenger: 61.0%   WTa challenger: 52.3%
//	ITF men: 57.9%   ITF women: 54.0%
//
// Surface adjustments: clay -4pp, hard 0pp, grass +6pp.
//
// Ported from v1 internal/algorithms/surface_markov.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents.
type SurfaceMarkov struct {
	// SeriesTicker is the series for this match (e.g. KXATPMATCH).
	SeriesTicker string
	// Surface is the court surface ("clay","grass","hard").
	Surface string
	// MinEdgeCents: minimum edge to fire (default 3).
	MinEdgeCents int
	// MaxMarketPriceCents: max price to buy at (default 85).
	MaxMarketPriceCents int
}

func NewSurfaceMarkov(series, surface string) *SurfaceMarkov {
	return &SurfaceMarkov{
		SeriesTicker:       series,
		Surface:            surface,
		MinEdgeCents:       3,
		MaxMarketPriceCents: 85,
	}
}

func (s *SurfaceMarkov) Name() string { return "surface-markov" }

// surfaceMarkovState holds per-match mutable state.
type surfaceMarkovState struct {
	fired bool
}

// seriesBasePServe maps series_ticker to base serve-win probability.
var seriesBasePServe = map[string]float64{
	"KXATPMATCH":           0.613,
	"KXWTAMATCH":           0.520,
	"KXATPCHALLENGERMATCH": 0.610,
	"KXWTACHALLENGERMATCH": 0.523,
	"KXITFMATCH":           0.579,
	"KXITFWMATCH":          0.540,
	"KXATPDOUBLES":         0.560,
	"KXWTADOUBLES":         0.500,
	"KXITFDOUBLES":         0.530,
	"KXITFWDOUBLES":        0.490,
	"KXTENNISEXHIBITION":   0.580,
	"KXCHALLENGERMATCH":    0.580,
}

const smDefaultPServe = 0.64

func surfaceAdjustment(surface string) float64 {
	switch surface {
	case "clay":
		return -0.04
	case "grass":
		return 0.06
	case "hard", "hard (indoor)":
		return 0.0
	default:
		return 0.0
	}
}

func pServeForContext(series, surface string) float64 {
	base := seriesBasePServe[series]
	if base == 0 {
		base = smDefaultPServe
	}
	p := base + surfaceAdjustment(surface)
	if p < 0.40 {
		p = 0.40
	}
	if p > 0.75 {
		p = 0.75
	}
	return p
}

func (s *SurfaceMarkov) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("surface-markov", nil)
		}
	}
	return nil
}

func (s *SurfaceMarkov) getOrCreateState(st *State) *surfaceMarkovState {
	if v := st.Get("surface-markov"); v != nil {
		return v.(*surfaceMarkovState)
	}
	ss := &surfaceMarkovState{}
	st.Set("surface-markov", ss)
	return ss
}

func (s *SurfaceMarkov) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	homeMkt, awayMkt := mv.MarketTickers[0], mv.MarketTickers[1]
	homePrice, homeOk := mv.Prices[homeMkt]
	awayPrice, awayOk := mv.Prices[awayMkt]
	if (!homeOk || homePrice <= 0) && (!awayOk || awayPrice <= 0) {
		return nil
	}

	pServe := pServeForContext(s.SeriesTicker, s.Surface)
	markov := pricing.NewMarkovModelWithProb(pServe)

	setsHome, setsAway := 0, 0
	if e.Point.HomeSetGames > e.Point.AwaySetGames {
		setsHome = 1
	} else if e.Point.AwaySetGames > e.Point.HomeSetGames {
		setsAway = 1
	}

	homeFV := markov.WinProbability(
		setsHome, setsAway, e.Point.HomeGames, e.Point.AwayGames,
		e.Point.HomePoints, e.Point.AwayPoints, e.Point.Server, e.Point.IsTiebreak,
	)
	homeFV = math.Max(0.01, math.Min(0.99, homeFV))
	awayFV := 1.0 - homeFV

	var intents []match.Intent
	if homeOk && homePrice > 0 {
		if in := s.checkEdge(homeMkt, homePrice, homeFV, e.Point.SetNumber); in != nil {
			intents = append(intents, *in)
		}
	}
	if ss.fired {
		return intents
	}
	if awayOk && awayPrice > 0 {
		if in := s.checkEdge(awayMkt, awayPrice, awayFV, e.Point.SetNumber); in != nil {
			intents = append(intents, *in)
		}
	}
	if len(intents) > 0 {
		ss.fired = true
	}
	return intents
}

func (s *SurfaceMarkov) checkEdge(mkt string, priceCents int, fairValue float64, setNum int) *match.Intent {
	if priceCents <= 0 || priceCents > s.MaxMarketPriceCents {
		return nil
	}
	fvCents := int(fairValue*100 + 1e-9)
	edgeCents := fvCents - priceCents
	if edgeCents < s.MinEdgeCents {
		return nil
	}
	return &match.Intent{
		MarketTicker: mkt,
		Strategy:     "surface-markov",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  int(fairValue*10000 + 1e-9),
		Reason:       fmt.Sprintf("smarkov_set%d_edge%dc", setNum, edgeCents),
	}
}
