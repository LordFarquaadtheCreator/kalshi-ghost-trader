package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestServer1530Scenarios(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(st *State)
		events    func() []match.Event
		wantCount int
		wantMkt   string
		wantPrice int
	}{
		{
			name: "fires_on_price_dip",
			setup: func(st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 56, "E1-A": 44}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// prime peak at 60, then dip to 56: dip = 4/60 = 667 bps (in [300,800])
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 100_000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 56, TS: 200_000},
				}
			},
			wantCount: 1,
			wantMkt:   "E1-H",
			wantPrice: 56,
		},
		{
			name: "no_fire_dip_too_small",
			setup: func(st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 59, "E1-A": 41}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// 60 -> 59: dip = 1/60 = 166 bps < 300
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 100_000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 59, TS: 200_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "no_fire_dip_too_large",
			setup: func(st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// 60 -> 50: dip = 10/60 = 1666 bps > 800
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 100_000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 200_000},
				}
			},
			wantCount: 0,
		},
		{
			name: "fires_on_1530_score_home_serving",
			setup: func(st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 60, "E1-A": 40}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				// prime peak via price update, then 15-30 score on home server
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 100_000},
					match.PointScored{
						EventTicker: "E1",
						Point: match.Point{
							SetNumber: 1, GameNumber: 3, PointNumber: 4,
							Server: 1, HomePoints: "15", AwayPoints: "30",
						},
						TS: 200_000,
					},
				}
			},
			wantCount: 1,
			wantMkt:   "E1-H",
			wantPrice: 60,
		},
		{
			name: "no_fire_score_peak_below_min",
			setup: func(st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 100_000},
					match.PointScored{
						EventTicker: "E1",
						Point: match.Point{
							SetNumber: 1, GameNumber: 3, PointNumber: 4,
							Server: 1, HomePoints: "15", AwayPoints: "30",
						},
						TS: 200_000,
					},
				}
			},
			wantCount: 0,
		},
		{
			name: "fires_only_once_across_paths",
			setup: func(st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 56, "E1-A": 44}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 200_000, "E1-A": 200_000}
			},
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 100_000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 56, TS: 200_000},
					match.PointScored{
						EventTicker: "E1",
						Point: match.Point{
							SetNumber: 1, GameNumber: 3, PointNumber: 4,
							Server: 1, HomePoints: "15", AwayPoints: "30",
						},
						TS: 210_000,
					},
				}
			},
			wantCount: 1, // dip fires, score suppressed
			wantMkt:   "E1-H",
			wantPrice: 56,
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
			s := NewServer1530()
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
				if got[0].Strategy != "server1530" {
					t.Errorf("Strategy = %s, want server1530", got[0].Strategy)
				}
				if got[0].ConvProbBps != 6200 {
					t.Errorf("ConvProbBps = %d, want 6200", got[0].ConvProbBps)
				}
			}
		})
	}
}
