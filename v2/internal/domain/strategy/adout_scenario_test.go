package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestAdOut(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*AdOut, *State)
		events      []match.Event
		wantIntents int
		wantAction  string
		wantMarket  string
	}{
		{
			name: "ad_out_home_serving_buy_then_sell_next_point",
			setup: func(s *AdOut, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Ad-out: home serving (server=1), away has "A".
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 3, PointNumber: 5,
						Server: 1, Scorer: 2,
						HomePoints: "40", AwayPoints: "A",
						HomeGames: 2, AwayGames: 1,
					},
					TS: 2000,
				},
				// Next point — away won (break). Sell.
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 1,
						Server: 2, Scorer: 1,
						HomePoints: "0", AwayPoints: "0",
						HomeGames: 3, AwayGames: 1,
					},
					TS: 3000,
				},
			},
			wantIntents: 1, // first event produces buy; second produces sell
			wantAction:  "buy",
			wantMarket:  "E1-A",
		},
		{
			name: "not_ad_out_no_intent",
			setup: func(s *AdOut, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Deuce, not ad-out.
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 3, PointNumber: 4,
						Server: 1, Scorer: 1,
						HomePoints: "40", AwayPoints: "40",
						HomeGames: 2, AwayGames: 1,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
		{
			name: "edge_too_small_no_intent",
			setup: func(s *AdOut, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				// Price 80 cents, fair value 82, edge = 2 < 3 min.
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 80}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 3, PointNumber: 5,
						Server: 1, Scorer: 2,
						HomePoints: "40", AwayPoints: "A",
						HomeGames: 2, AwayGames: 1,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
		{
			name: "tiebreak_skipped",
			setup: func(s *AdOut, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 13, PointNumber: 5,
						Server: 1, Scorer: 2,
						HomePoints: "40", AwayPoints: "A",
						HomeGames: 6, AwayGames: 6,
						IsTiebreak: true,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
		{
			name: "stale_price_no_buy",
			setup: func(s *AdOut, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 3, PointNumber: 5,
						Server: 1, Scorer: 2,
						HomePoints: "40", AwayPoints: "A",
						HomeGames: 2, AwayGames: 1,
					},
					TS: 70000, // price TS is 1000, 69s stale
				},
			},
			wantIntents: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewAdOut()
			st := &State{
				MatchView:     MatchView{Prices: map[string]int{}, PriceTS: map[string]int64{}},
				StrategyState: map[string]any{},
			}
			tc.setup(s, st)

			var lastIntents []match.Intent
			for _, ev := range tc.events {
				intents := s.OnEvent(ev, st)
				if len(intents) > 0 {
					lastIntents = intents
				}
			}

			// For the buy-then-sell scenario, check the first event produces buy.
			if tc.name == "ad_out_home_serving_buy_then_sell_next_point" {
				// First event should produce buy.
				st1 := &State{
					MatchView:     st.MatchView,
					StrategyState: map[string]any{},
				}
				tc.setup(s, st1)
				buyIntents := s.OnEvent(tc.events[0], st1)
				if len(buyIntents) != 1 {
					t.Fatalf("first event: got %d intents, want 1", len(buyIntents))
				}
				if buyIntents[0].Action != "buy" {
					t.Errorf("first event Action = %s, want buy", buyIntents[0].Action)
				}
				if buyIntents[0].MarketTicker != "E1-A" {
					t.Errorf("first event MarketTicker = %s, want E1-A", buyIntents[0].MarketTicker)
				}
				// Second event should produce sell.
				sellIntents := s.OnEvent(tc.events[1], st1)
				if len(sellIntents) != 1 {
					t.Fatalf("second event: got %d intents, want 1", len(sellIntents))
				}
				if sellIntents[0].Action != "sell" {
					t.Errorf("second event Action = %s, want sell", sellIntents[0].Action)
				}
				return
			}

			if len(lastIntents) != tc.wantIntents {
				t.Fatalf("got %d intents, want %d", len(lastIntents), tc.wantIntents)
			}
			if tc.wantIntents > 0 {
				if lastIntents[0].Action != tc.wantAction {
					t.Errorf("Action = %s, want %s", lastIntents[0].Action, tc.wantAction)
				}
				if lastIntents[0].MarketTicker != tc.wantMarket {
					t.Errorf("MarketTicker = %s, want %s", lastIntents[0].MarketTicker, tc.wantMarket)
				}
			}
		})
	}
}
