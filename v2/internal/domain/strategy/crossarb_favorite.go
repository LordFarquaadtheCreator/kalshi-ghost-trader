package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// CrossArbFavorite is the directional favorite-fade variant of cross-arb.
// When yesSum > 1.0 + threshold AND one NO side is cheap (favorite
// overpriced), buys NO of the favorite only. Skips the expensive NO hedge —
// empirical data shows it loses 86% of the time.
//
// Backtest (9 days, 1675 cross-arb orders): buy_no with NO<30c hit 75.9%,
// ROI 373%, vs buy_no with NO>=50c hit 13.6%, ROI -57%. Dropping the
// expensive hedge lifts ROI from 60% to 373% on the same signal.
//
// Ported from v1 internal/algorithms/crossarb_favorite.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents,
// prices in cents, ConvProb in bps. Per-match "fired" flag stored via
// State.Get/Set.
type CrossArbFavorite struct {
	cfg CrossArbFavoriteConfig
}

// CrossArbFavoriteConfig controls the directional favorite-fade variant.
type CrossArbFavoriteConfig struct {
	MinEdgeCents   int    // minimum yesSum-1.0 edge in cents to trigger (default 2)
	MaxNOPriceCents int   // only fire when favorite's NO price is at or below this (default 30 = favorite YES >= 70c)
	Label          string // strategy label
}

// DefaultCrossArbFavoriteConfig returns sensible defaults.
func DefaultCrossArbFavoriteConfig() CrossArbFavoriteConfig {
	return CrossArbFavoriteConfig{
		MinEdgeCents:    2,
		MaxNOPriceCents: 30,
		Label:           "cross-arb-favorite",
	}
}

// NewCrossArbFavorite creates a cross-arb-favorite strategy.
func NewCrossArbFavorite(cfg CrossArbFavoriteConfig) *CrossArbFavorite {
	return &CrossArbFavorite{cfg: cfg}
}

func (s *CrossArbFavorite) Name() string { return s.cfg.Label }

func (s *CrossArbFavorite) OnEvent(ev match.Event, st *State) []match.Intent {
	e, ok := ev.(match.PriceUpdate)
	if !ok {
		return nil
	}
	return s.onPrice(e, st)
}

func (s *CrossArbFavorite) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Fire once per match.
	if v := st.Get(s.cfg.Label); v != nil {
		if fired, _ := v.(bool); fired {
			return nil
		}
	}

	homeMkt, awayMkt := mv.MarketTickers[0], mv.MarketTickers[1]
	homeCents, okH := mv.Prices[homeMkt]
	awayCents, okA := mv.Prices[awayMkt]
	if !okH || !okA || homeCents <= 0 || awayCents <= 0 {
		return nil
	}

	// Staleness guard.
	if hts, has := mv.PriceTS[homeMkt]; has && e.TS-hts > cafPriceStaleMS {
		return nil
	}
	if ats, has := mv.PriceTS[awayMkt]; has && e.TS-ats > cafPriceStaleMS {
		return nil
	}

	yesSumCents := homeCents + awayCents
	noEdgeCents := yesSumCents - 100
	if noEdgeCents < s.cfg.MinEdgeCents {
		return nil
	}

	// Favorite = higher YES price = cheaper NO. Fade the favorite's NO only.
	favMkt, favCents := homeMkt, homeCents
	if awayCents > homeCents {
		favMkt, favCents = awayMkt, awayCents
	}
	noCents := 100 - favCents
	if noCents > s.cfg.MaxNOPriceCents {
		return nil
	}

	st.Set(s.cfg.Label, true)
	return []match.Intent{{
		MarketTicker: favMkt,
		Strategy:     s.cfg.Label,
		Action:       "buy_no",
		PriceCents:   noCents,
		ConvProbBps:  favCents * 100, // NO wins when YES loses
		Reason:       "crossarbfav_buy_no_edge" + intToStr(noEdgeCents) + "c",
	}}
}

const cafPriceStaleMS = 60_000
