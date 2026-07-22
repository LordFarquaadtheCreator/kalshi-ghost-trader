package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// CrossArb monitors both YES markets for the same event. When the sum of YES
// prices < 1.0 - threshold, buys both YES (guaranteed profit). When the sum
// > 1.0 + threshold, buys both NO (guaranteed profit). Fires once per match.
//
// Ported from v1 internal/algorithms/crossarb.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents, prices in cents,
// ConvProb in bps. Per-match "fired" flag stored via State.Get/Set.
type CrossArb struct {
	cfg CrossArbConfig
}

// CrossArbConfig controls the cross-side arbitrage strategy.
type CrossArbConfig struct {
	MinEdgeCents int    // minimum arb edge to trigger (default 2 = 2c per pair)
	Label        string // strategy label
}

// DefaultCrossArbConfig returns sensible defaults.
func DefaultCrossArbConfig() CrossArbConfig {
	return CrossArbConfig{
		MinEdgeCents: 2,
		Label:        "cross-arb",
	}
}

// NewCrossArb creates a cross-arb strategy.
func NewCrossArb(cfg CrossArbConfig) *CrossArb { return &CrossArb{cfg: cfg} }

func (s *CrossArb) Name() string { return s.cfg.Label }

func (s *CrossArb) OnEvent(ev match.Event, st *State) []match.Intent {
	e, ok := ev.(match.PriceUpdate)
	if !ok {
		return nil
	}
	return s.onPrice(e, st)
}

func (s *CrossArb) onPrice(e match.PriceUpdate, st *State) []match.Intent {
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

	// Staleness guard: both prices must be fresh relative to this update.
	if hts, has := mv.PriceTS[homeMkt]; has && e.TS-hts > caPriceStaleMS {
		return nil
	}
	if ats, has := mv.PriceTS[awayMkt]; has && e.TS-ats > caPriceStaleMS {
		return nil
	}

	yesSumCents := homeCents + awayCents
	edgeCents := 100 - yesSumCents // buy both YES edge

	if edgeCents >= s.cfg.MinEdgeCents {
		st.Set(s.cfg.Label, true)
		return []match.Intent{
			{
				MarketTicker: homeMkt,
				Strategy:     s.cfg.Label,
				Action:       "buy",
				PriceCents:   homeCents,
				ConvProbBps:  10000 - homeCents*100, // ≈ 1 - price
				Reason:       "crossarb_buy_yes_edge" + intToStr(edgeCents) + "c",
			},
			{
				MarketTicker: awayMkt,
				Strategy:     s.cfg.Label,
				Action:       "buy",
				PriceCents:   awayCents,
				ConvProbBps:  10000 - awayCents*100, // ≈ 1 - price
				Reason:       "crossarb_buy_yes_edge" + intToStr(edgeCents) + "c",
			},
		}
	}

	// NO arb: yesSum > 1.0 → buy both NO. NO price ≈ 1 - YES price.
	noEdgeCents := yesSumCents - 100
	if noEdgeCents >= s.cfg.MinEdgeCents {
		st.Set(s.cfg.Label, true)
		return []match.Intent{
			{
				MarketTicker: homeMkt,
				Strategy:     s.cfg.Label,
				Action:       "buy_no",
				PriceCents:   100 - homeCents, // NO price in cents
				ConvProbBps:  homeCents * 100, // NO wins when YES loses
				Reason:       "crossarb_buy_no_edge" + intToStr(noEdgeCents) + "c",
			},
			{
				MarketTicker: awayMkt,
				Strategy:     s.cfg.Label,
				Action:       "buy_no",
				PriceCents:   100 - awayCents, // NO price in cents
				ConvProbBps:  awayCents * 100, // NO wins when YES loses
				Reason:       "crossarb_buy_no_edge" + intToStr(noEdgeCents) + "c",
			},
		}
	}

	return nil
}

const caPriceStaleMS = 60_000
