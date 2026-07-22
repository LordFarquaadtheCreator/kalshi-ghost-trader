package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestBreakPoint(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*BreakPoint, *State)
		events      []match.Event
		wantIntents int
		wantAction  string
		wantMarket  string
	}{
		{
			name: "break_point_away_returning_buy_away_market",
			setup: func(s *BreakPoint, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				// Away market cheap relative to Markov fair value.
				st.MatchView.Prices = map[string]int{"E1-H": 80, "E1-A": 20}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Home serving, away has break point (30-40).
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 3,
						Server: 1, Scorer: 2,
						HomePoints: "30", AwayPoints: "40",
						HomeGames: 2, AwayGames: 1,
						IsBreakPoint: true,
					},
					TS: 2000,
				},
			},
			wantIntents: 1,
			wantAction:  "buy",
			wantMarket:  "E1-A",
		},
		{
			name: "break_point_home_returning_buy_home_market",
			setup: func(s *BreakPoint, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 20, "E1-A": 80}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Away serving, home has break point (40-30).
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 3,
						Server: 2, Scorer: 1,
						HomePoints: "40", AwayPoints: "30",
						HomeGames: 1, AwayGames: 2,
						IsBreakPoint: true,
					},
					TS: 2000,
				},
			},
			wantIntents: 1,
			wantAction:  "buy",
			wantMarket:  "E1-H",
		},
		{
			name: "not_break_point_no_intent",
			setup: func(s *BreakPoint, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// 30-30, not a break point.
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 2,
						Server: 1, Scorer: 1,
						HomePoints: "30", AwayPoints: "30",
						HomeGames: 2, AwayGames: 1,
						IsBreakPoint: false,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
		{
			name: "tiebreak_skipped",
			setup: func(s *BreakPoint, st *State) {
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
						HomePoints: "30", AwayPoints: "40",
						HomeGames: 6, AwayGames: 6,
						IsBreakPoint: true,
						IsTiebreak:   true,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
		{
			name: "stale_price_no_intent",
			setup: func(s *BreakPoint, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 80, "E1-A": 20}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 3,
						Server: 1, Scorer: 2,
						HomePoints: "30", AwayPoints: "40",
						HomeGames: 2, AwayGames: 1,
						IsBreakPoint: true,
					},
					TS: 70000, // price TS 1000, 69s stale
				},
			},
			wantIntents: 0,
		},
		{
			name: "price_above_max_no_intent",
			setup: func(s *BreakPoint, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				// Away at 65 cents — above MaxPriceCents (60).
				st.MatchView.Prices = map[string]int{"E1-H": 35, "E1-A": 65}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 3,
						Server: 1, Scorer: 2,
						HomePoints: "30", AwayPoints: "40",
						HomeGames: 2, AwayGames: 1,
						IsBreakPoint: true,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewBreakPoint()
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
