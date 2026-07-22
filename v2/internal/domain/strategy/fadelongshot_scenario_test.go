package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestFadeLongshotScenarios(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(st *State)
		events    func() []match.Event
		wantCount int
		wantMkt   string
		wantPrice int
	}{
		{
			name: "fires_on_favorite_in_window",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 93, "E1-A": 7}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 100_000, "E1-A": 100_000}
			},
			events: func() []match.Event {
				// now = 200_000, window 900s = 900_000ms, entryTs = 100_000
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 93, TS: 200_000},
				}
			},
			wantCount: 1,
			wantMkt:   "E1-H",
			wantPrice: 93,
		},
		{
			name: "no_fire_before_window",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 93, "E1-A": 7}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 50_000, "E1-A": 50_000}
			},
			events: func() []match.Event {
				// entryTs = 100_000, now = 50_000 -> before window
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 93, TS: 50_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "no_fire_favorite_below_min",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 40, "E1-A": 45}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-A", PriceCents: 45, TS: 200_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "fires_only_once",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 93, "E1-A": 7}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 93, TS: 200_000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 92, TS: 210_000},
				}
			},
			wantCount: 1, // first fires, second suppressed
			wantMkt:   "E1-H",
			wantPrice: 93,
		},
		{
			name: "dynamic_convprob_match_point",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 94, "E1-A": 6}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// score update sets match point -> convProb 9950 bps, edge = 99-94 = 5
				return []match.Event{
					match.PointScored{
						EventTicker: "E1",
						Point: match.Point{
							SetNumber: 2, GameNumber: 9, PointNumber: 1,
							HomeGames: 5, AwayGames: 3,
							HomeSetGames: 1, AwaySetGames: 0,
							IsMatchPoint: true,
						},
						TS: 200_000,
					},
				}
			},
			wantCount: 1,
			wantMkt:   "E1-H",
			wantPrice: 94,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{StrategyState: map[string]any{}}
			st.MatchView = MatchView{
				Prices:   map[string]int{},
				PriceTS:  map[string]int64{},
			}
			tc.setup(st)
			s := NewFadeLongshot()
			var got []match.Intent
			for _, ev := range tc.events() {
				intents := s.OnEvent(ev, st)
				got = append(got, intents...)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d intents, want %d", len(got), tc.wantCount)
			}
			if tc.wantCount == 1 {
				if got[0].MarketTicker != tc.wantMkt {
					t.Errorf("MarketTicker = %s, want %s", got[0].MarketTicker, tc.wantMkt)
				}
				if got[0].PriceCents != tc.wantPrice {
					t.Errorf("PriceCents = %d, want %d", got[0].PriceCents, tc.wantPrice)
				}
				if got[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", got[0].Action)
				}
				if got[0].Strategy != "fadelongshot" {
					t.Errorf("Strategy = %s, want fadelongshot", got[0].Strategy)
				}
			}
		})
	}
}
