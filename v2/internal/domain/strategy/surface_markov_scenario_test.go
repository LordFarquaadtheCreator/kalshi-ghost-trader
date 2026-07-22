package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestSurfaceMarkovScenarios(t *testing.T) {
	tests := []struct {
		name     string
		series   string
		surface  string
		homePx   int
		awayPx   int
		point    match.Point
		wantLen  int
		wantMkt  string
	}{
		{
			name:    "atp_hard_home_undervalued_fires",
			series:  "KXATPMATCH",
			surface: "hard",
			homePx:  30,
			awayPx:  70,
			// Early set, home serving, 0-0. Markov with pServe=0.613 → home FV high.
			point: match.Point{
				SetNumber: 1, GameNumber: 1, PointNumber: 1,
				HomeGames: 0, AwayGames: 0, Server: 1,
				HomePoints: "0", AwayPoints: "0",
			},
			wantLen: 1,
			wantMkt: "E1-H",
		},
		{
			name:    "wta_clay_both_sides_overpriced_no_fire",
			series:  "KXWTAMATCH",
			surface: "clay",
			homePx:  85,
			awayPx:  85,
			// pServe=0.48 → home FV < 50c. Both priced at 85c → no edge.
			point: match.Point{
				SetNumber: 1, GameNumber: 1, PointNumber: 1,
				HomeGames: 0, AwayGames: 0, Server: 1,
				HomePoints: "0", AwayPoints: "0",
			},
			wantLen: 0,
		},
		{
			name:    "grass_home_serving_edge_fires",
			series:  "KXATPMATCH",
			surface: "grass",
			homePx:  40,
			awayPx:  60,
			point: match.Point{
				SetNumber: 1, GameNumber: 1, PointNumber: 1,
				HomeGames: 0, AwayGames: 0, Server: 1,
				HomePoints: "0", AwayPoints: "0",
			},
			wantLen: 1,
			wantMkt: "E1-H",
		},
		{
			name:    "fires_once_then_dedup",
			series:  "KXATPMATCH",
			surface: "hard",
			homePx:  30,
			awayPx:  70,
			point: match.Point{
				SetNumber: 1, GameNumber: 1, PointNumber: 1,
				HomeGames: 0, AwayGames: 0, Server: 1,
				HomePoints: "0", AwayPoints: "0",
			},
			wantLen: 1,
			wantMkt: "E1-H",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSurfaceMarkov(tc.series, tc.surface)
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{"E1-H": tc.homePx, "E1-A": tc.awayPx},
					PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
				},
				StrategyState: map[string]any{},
			}

			ev := match.PointScored{EventTicker: "E1", Point: tc.point, TS: 1000}
			got := s.OnEvent(ev, st)

			if tc.name == "fires_once_then_dedup" {
				// Fire second time — should be deduped.
				got2 := s.OnEvent(ev, st)
				if len(got2) != 0 {
					t.Fatalf("second event: got %d intents, want 0 (dedup)", len(got2))
				}
			}

			if len(got) != tc.wantLen {
				t.Fatalf("got %d intents, want %d", len(got), tc.wantLen)
			}
			if tc.wantLen > 0 && got[0].MarketTicker != tc.wantMkt {
				t.Errorf("MarketTicker = %s, want %s", got[0].MarketTicker, tc.wantMkt)
			}
		})
	}
}
