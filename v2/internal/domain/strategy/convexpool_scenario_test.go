package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestConvexPoolScenarios(t *testing.T) {
	cases := []struct {
		name      string
		point     match.Point
		ts        int64
		prices    map[string]int
		priceTS   map[string]int64
		tickers   []string
		wantCount int
		wantMkt   string // empty = don't check
	}{
		{
			name: "edge_triggers_buy_home",
			// Home dominating set 1: 5-0, 40-0, home serving → fvHome high.
			// Market prices home at 50c (cheap) → convex blend exceeds price.
			point: match.Point{
				SetNumber: 1, GameNumber: 6, PointNumber: 1,
				HomeGames: 5, AwayGames: 0,
				HomePoints: "40", AwayPoints: "0",
				Server: 1, HomeSetGames: 0, AwaySetGames: 0,
			},
			ts:        2000,
			prices:    map[string]int{"E1-H": 50, "E1-A": 90},
			priceTS:   map[string]int64{"E1-H": 1000, "E1-A": 1000},
			tickers:   []string{"E1-H", "E1-A"},
			wantCount: 1,
			wantMkt:   "E1-H",
		},
		{
			name: "no_edge_when_fv_matches_market",
			// Neutral state 0-0, 0-0, 0-0 → fv ≈ 0.5. Prices 50/50 → blend 0.5, edge 0.
			point: match.Point{
				SetNumber: 1, GameNumber: 1, PointNumber: 1,
				HomeGames: 0, AwayGames: 0,
				HomePoints: "0", AwayPoints: "0",
				Server: 1, HomeSetGames: 0, AwaySetGames: 0,
			},
			ts:        2000,
			prices:    map[string]int{"E1-H": 50, "E1-A": 50},
			priceTS:   map[string]int64{"E1-H": 1000, "E1-A": 1000},
			tickers:   []string{"E1-H", "E1-A"},
			wantCount: 0,
		},
		{
			name: "stale_price_no_intent",
			// Same dominating state as edge case, but price TS is 70s old → stale.
			point: match.Point{
				SetNumber: 1, GameNumber: 6, PointNumber: 1,
				HomeGames: 5, AwayGames: 0,
				HomePoints: "40", AwayPoints: "0",
				Server: 1, HomeSetGames: 0, AwaySetGames: 0,
			},
			ts:        72000, // priceTS 1000 → 71s stale
			prices:    map[string]int{"E1-H": 50, "E1-A": 90},
			priceTS:   map[string]int64{"E1-H": 1000, "E1-A": 1000},
			tickers:   []string{"E1-H", "E1-A"},
			wantCount: 0,
		},
		{
			name: "price_below_min_no_intent",
			// Edge exists but home price 3c < MinPriceCents (5) → skip.
			point: match.Point{
				SetNumber: 1, GameNumber: 6, PointNumber: 1,
				HomeGames: 5, AwayGames: 0,
				HomePoints: "40", AwayPoints: "0",
				Server: 1, HomeSetGames: 0, AwaySetGames: 0,
			},
			ts:        2000,
			prices:    map[string]int{"E1-H": 3, "E1-A": 97},
			priceTS:   map[string]int64{"E1-H": 1000, "E1-A": 1000},
			tickers:   []string{"E1-H", "E1-A"},
			wantCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewConvexPool(DefaultConvexPoolConfig())
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: tc.tickers,
					Prices:        tc.prices,
					PriceTS:       tc.priceTS,
				},
				StrategyState: map[string]any{},
			}
			intents := s.OnEvent(match.PointScored{
				EventTicker: "E1",
				Point:       tc.point,
				TS:          tc.ts,
			}, st)

			if len(intents) != tc.wantCount {
				t.Fatalf("got %d intents, want %d (intents=%+v)", len(intents), tc.wantCount, intents)
			}
			if tc.wantMkt != "" && len(intents) > 0 {
				if intents[0].MarketTicker != tc.wantMkt {
					t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tc.wantMkt)
				}
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
				if intents[0].ConvProbBps <= 0 || intents[0].ConvProbBps > 10000 {
					t.Errorf("ConvProbBps = %d, want (0,10000]", intents[0].ConvProbBps)
				}
			}
		})
	}
}
