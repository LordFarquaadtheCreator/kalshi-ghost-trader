package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestBreakBack(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*BreakBack, *State)
		events      []match.Event
		wantIntents int
		wantAction  string
		wantMarket  string
	}{
		{
			name: "score_based_break_home_served_buy_home_market",
			setup: func(s *BreakBack, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 40, "E1-A": 60}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Price updates to establish peak price for home market.
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 500},
				// Break: server=1 (home), scorer=2 (away won), IsBreakPoint=true.
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
			wantMarket:  "E1-H",
		},
		{
			name: "price_based_drop_triggers_buy",
			setup: func(s *BreakBack, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{}
				st.MatchView.PriceTS = map[string]int64{}
			},
			events: []match.Event{
				// Peak at 50 cents.
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
				// Drop to 40 cents — 20% drop (>= 15%).
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 2000},
			},
			wantIntents: 1,
			wantAction:  "buy",
			wantMarket:  "E1-H",
		},
		{
			name: "no_break_scorer_equals_server_no_intent",
			setup: func(s *BreakBack, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 40, "E1-A": 60}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 500},
				// Server held: scorer == server.
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 4, PointNumber: 3,
						Server: 1, Scorer: 1,
						HomePoints: "40", AwayPoints: "30",
						HomeGames: 2, AwayGames: 1,
						IsBreakPoint: true,
					},
					TS: 2000,
				},
			},
			wantIntents: 0,
		},
		{
			name: "already_fired_no_second_entry",
			setup: func(s *BreakBack, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 40, "E1-A": 60}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 500},
				// First break — fires.
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
				// Second break — should not fire again.
				match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 1, GameNumber: 6, PointNumber: 3,
						Server: 1, Scorer: 2,
						HomePoints: "30", AwayPoints: "40",
						HomeGames: 4, AwayGames: 1,
						IsBreakPoint: true,
					},
					TS: 3000,
				},
			},
			wantIntents: 1,
			wantAction:  "buy",
			wantMarket:  "E1-H",
		},
		{
			name: "peak_too_low_no_intent",
			setup: func(s *BreakBack, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 25, "E1-A": 75}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Peak only 25 cents — below MinPeakPrice (30).
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 25, TS: 500},
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
		{
			name: "stale_price_no_intent",
			setup: func(s *BreakBack, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 40, "E1-A": 60}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 500},
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewBreakBack()
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
