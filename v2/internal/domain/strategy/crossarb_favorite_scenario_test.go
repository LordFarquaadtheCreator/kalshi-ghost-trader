package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestCrossArbFavoriteScenarios(t *testing.T) {
	cases := []struct {
		name       string
		homeCents  int
		awayCents  int
		wantCount  int
		wantMkt    string // empty = don't check
		wantAction string
	}{
		{
			name:       "buy_favorite_no_when_cheap",
			homeCents:  70,
			awayCents:  40, // sum 110, fav=home 70c YES → NO 30c (== max 30)
			wantCount:  1,
			wantMkt:    "E1-H",
			wantAction: "buy_no",
		},
		{
			name:       "favorite_is_away_side",
			homeCents:  35,
			awayCents:  75, // sum 110, fav=away 75c YES → NO 25c
			wantCount:  1,
			wantMkt:    "E1-A",
			wantAction: "buy_no",
		},
		{
			name:      "no_too_expensive_no_intent",
			homeCents: 60,
			awayCents: 55, // sum 115, fav=home 60c → NO 40c > max 30
			wantCount: 0,
		},
		{
			name:      "sum_below_threshold_no_intent",
			homeCents: 50,
			awayCents: 51, // sum 101, noEdge 1c < min 2
			wantCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCrossArbFavorite(DefaultCrossArbFavoriteConfig())
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
			if tc.wantMkt != "" && len(intents) > 0 {
				if intents[0].MarketTicker != tc.wantMkt {
					t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tc.wantMkt)
				}
				if intents[0].Action != tc.wantAction {
					t.Errorf("Action = %s, want %s", intents[0].Action, tc.wantAction)
				}
				if intents[0].Strategy != "cross-arb-favorite" {
					t.Errorf("Strategy = %s, want cross-arb-favorite", intents[0].Strategy)
				}
				if intents[0].ConvProbBps < 0 || intents[0].ConvProbBps > 10000 {
					t.Errorf("ConvProbBps = %d out of range", intents[0].ConvProbBps)
				}
			}
		})
	}
}

// TestCrossArbFavoriteFiresOnce: after first fire, subsequent updates suppressed.
func TestCrossArbFavoriteFiresOnce(t *testing.T) {
	s := NewCrossArbFavorite(DefaultCrossArbFavoriteConfig())
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 70, "E1-A": 40},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	first := s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 2000}, st)
	if len(first) != 1 {
		t.Fatalf("first fire: got %d intents, want 1", len(first))
	}

	// Bigger edge later → suppressed.
	st.MatchView.Prices["E1-H"] = 80
	second := s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 80, TS: 3000}, st)
	if len(second) != 0 {
		t.Fatalf("second fire: got %d intents, want 0 (fire-once)", len(second))
	}
}
