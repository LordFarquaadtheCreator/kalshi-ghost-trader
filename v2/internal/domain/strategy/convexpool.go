package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/pricing"
)

// ConvexPool blends Markov fair value with market price using a convex
// combination: blended = α * markov + (1-α) * market. When Markov and market
// agree, no edge → no trade. When they diverge, the convex blend gives a
// tempered edge signal. α controls model vs market weight.
//
// Fires on every point update (not just break/set/match points), making it a
// general-purpose fair-value trader. Ported from v1 internal/algorithms/convexpool.go
// — decision logic preserved, mutexes removed, EmitOrder replaced with returned
// intents, prices in cents, ConvProb in bps.
type ConvexPool struct {
	cfg   ConvexPoolConfig
	model *pricing.MarkovModel
}

// ConvexPoolConfig configures the convex pool strategy.
type ConvexPoolConfig struct {
	PServe        float64 // serve point win probability
	Alpha         float64 // model weight (0-1). 0.5 = equal blend
	MinEdgeCents  int     // minimum edge to trigger
	MinPriceCents int     // minimum market price in cents
	MaxPriceCents int     // maximum market price in cents (0 = no cap)
	Label         string
}

// DefaultConvexPoolConfig returns sensible defaults.
func DefaultConvexPoolConfig() ConvexPoolConfig {
	return ConvexPoolConfig{
		PServe:        0.64,
		Alpha:         0.5,
		MinEdgeCents:  3,
		MinPriceCents: 5,  // 0.05
		MaxPriceCents: 95, // 0.95
		Label:         "convexpool",
	}
}

const cpPriceStaleMS = 60_000

// NewConvexPool creates a convex pool strategy.
func NewConvexPool(cfg ConvexPoolConfig) *ConvexPool {
	return &ConvexPool{
		cfg:   cfg,
		model: pricing.NewMarkovModelWithProb(cfg.PServe),
	}
}

func (s *ConvexPool) Name() string { return s.cfg.Label }

func (s *ConvexPool) OnEvent(ev match.Event, st *State) []match.Intent {
	e, ok := ev.(match.PointScored)
	if !ok {
		return nil
	}
	return s.onPoint(e, st)
}

func (s *ConvexPool) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Sets already won come from the point's set-games fields (points arrive
	// in order, so the latest point carries the authoritative set count).
	setsHome := e.Point.HomeSetGames
	setsAway := e.Point.AwaySetGames

	fvHome := s.model.FairValue(
		setsHome, setsAway,
		e.Point.HomeGames, e.Point.AwayGames,
		e.Point.HomePoints, e.Point.AwayPoints,
		e.Point.Server, e.Point.IsTiebreak,
	)
	fvAway := 1.0 - fvHome

	var intents []match.Intent
	for i, mkt := range mv.MarketTickers {
		if i > 1 {
			break
		}
		fv := fvHome
		side := "home"
		if i == 1 {
			fv = fvAway
			side = "away"
		}

		priceCents, ok := mv.Prices[mkt]
		if !ok || priceCents <= 0 {
			continue
		}

		priceTS, hasTS := mv.PriceTS[mkt]
		if !hasTS {
			continue
		}
		if e.TS-priceTS > cpPriceStaleMS {
			continue
		}

		// Convex blend: tempered fair value. Price is in cents → fraction.
		priceF := float64(priceCents) / 100.0
		blended := s.cfg.Alpha*fv + (1-s.cfg.Alpha)*priceF
		edgeCents := int(blended*100) - priceCents

		if edgeCents < s.cfg.MinEdgeCents {
			continue
		}
		if priceCents < s.cfg.MinPriceCents {
			continue
		}
		if s.cfg.MaxPriceCents > 0 && priceCents > s.cfg.MaxPriceCents {
			continue
		}

		intents = append(intents, match.Intent{
			MarketTicker: mkt,
			Strategy:     s.cfg.Label,
			Action:       "buy",
			PriceCents:   priceCents,
			ConvProbBps:  int(blended * 10000),
			Reason: "convex_" + side + "_set" + intToStr(e.Point.SetNumber) +
				"_game" + intToStr(e.Point.GameNumber) + "_pt" + intToStr(e.Point.PointNumber),
		})
	}
	return intents
}
