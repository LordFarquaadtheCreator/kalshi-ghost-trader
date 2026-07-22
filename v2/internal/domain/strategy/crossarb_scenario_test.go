package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestCrossArbScenarios(t *testing.T) {
	cases := []struct {
		name       string
		homeCents  int
		awayCents  int
		wantCount  int
		wantAction string // empty = don't check
		wantReason string // substring check, empty = skip
	}{
		{
			name:       "buy_both_yes_when_sum_below_100",
			homeCents:  40,
			awayCents:  50, // sum 90, edge 10c
			wantCount:  2,
			wantAction: "buy",
			wantReason: "buy_yes",
		},
		{
			name:       "buy_both_no_when_sum_above_100",
			homeCents:  70,
			awayCents:  40, // sum 110, noEdge 10c
			wantCount:  2,
			wantAction: "buy_no",
			wantReason: "buy_no",
		},
		{
			name:      "no_arb_when_sum_at_100",
			homeCents: 50,
			awayCents: 50, // sum 100, edge 0
			wantCount: 0,
		},
		{
			name:      "edge_below_threshold_no_intent",
			homeCents: 49,
			awayCents: 50, // sum 99, edge 1c < MinEdgeCents(2)
			wantCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCrossArb(DefaultCrossArbConfig())
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices: map[string]int{
						"E1-H": tc.homeCents,
						"E1-A": tc.awayCents,
					},
					PriceTS: map[string]int64{"E1-H": 1000, "E1-A": 1000},
				},
				StrategyState: map[string]any{},
			}

			intents := s.OnEvent(match.PriceUpdate{
				MarketTicker: "E1-H",
				PriceCents:   tc.homeCents,
				TS:           2000,
			}, st)

			if len(intents) != tc.wantCount {
				t.Fatalf("got %d intents, want %d (intents=%+v)", len(intents), tc.wantCount, intents)
			}
			if tc.wantAction != "" {
				for _, in := range intents {
					if in.Action != tc.wantAction {
						t.Errorf("Action = %s, want %s", in.Action, tc.wantAction)
					}
					if in.Strategy != "cross-arb" {
						t.Errorf("Strategy = %s, want cross-arb", in.Strategy)
					}
					if in.ConvProbBps < 0 || in.ConvProbBps > 10000 {
						t.Errorf("ConvProbBps = %d out of range", in.ConvProbBps)
					}
				}
			}
		})
	}
}

// TestCrossArbFiresOnce: after first fire, subsequent price updates produce no
// new intents.
func TestCrossArbFiresOnce(t *testing.T) {
	s := NewCrossArb(DefaultCrossArbConfig())
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 40, "E1-A": 50},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	first := s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 2000}, st)
	if len(first) != 2 {
		t.Fatalf("first fire: got %d intents, want 2", len(first))
	}

	// Second update with an even bigger edge → should be suppressed.
	st.MatchView.Prices["E1-H"] = 30
	second := s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 3000}, st)
	if len(second) != 0 {
		t.Fatalf("second fire: got %d intents, want 0 (fire-once)", len(second))
	}
}
