package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestNoFadeScenarios(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(st *State)
		events    func() []match.Event
		wantCount int
		wantMkt   string
		wantPrice int
	}{
		{
			name: "fires_when_underdog_very_cheap",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 94, "E1-A": 6}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// underdog 6c > MaxNoPrice 5c -> no fire
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-A", PriceCents: 6, TS: 200_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "fires_when_underdog_at_threshold",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 93, "E1-A": 5}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// underdog 5c <= 5c, fav 93c, convProb 9500 bps, edge = 95-93 = 2
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
				st.MatchView.Prices = map[string]int{"E1-H": 93, "E1-A": 5}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 50_000, "E1-A": 50_000}
			},
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 93, TS: 50_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "no_fire_edge_below_min",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 95, "E1-A": 5}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// convProb 9500 bps = 95c, fav 95c, edge = 0 -> no fire
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 95, TS: 200_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "fires_only_once",
			setup: func(st *State) {
				st.MatchView.OccurrenceTS = 1_000_000
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 93, "E1-A": 5}
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{StrategyState: map[string]any{}}
			st.MatchView = MatchView{
				Prices:  map[string]int{},
				PriceTS: map[string]int64{},
			}
			tc.setup(st)
			s := NewNoFade()
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
				if got[0].Strategy != "nofade" {
					t.Errorf("Strategy = %s, want nofade", got[0].Strategy)
				}
				// convProb = (100 - 5) * 100 = 9500
				if got[0].ConvProbBps != 9500 {
					t.Errorf("ConvProbBps = %d, want 9500", got[0].ConvProbBps)
				}
			}
		})
	}
}
