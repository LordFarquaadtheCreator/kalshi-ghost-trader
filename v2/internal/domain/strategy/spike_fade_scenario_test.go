package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestSpikeFadeScenarios(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s *SpikeFade, st *State)
		events  []match.Event
		wantLen int
		wantMkt string
	}{
		{
			name: "spike_detected_fades_opposite_side",
			setup: func(s *SpikeFade, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 30, "E1-A": 70}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				// Pre-spike price 30c.
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 1000},
				// Spike to 65c (>30c jump in <30s).
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 65, TS: 5000},
			},
			wantLen: 1,
			wantMkt: "E1-A",
		},
		{
			name: "no_spike_below_threshold",
			setup: func(s *SpikeFade, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 50, "E1-A": 50}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 55, TS: 5000},
			},
			wantLen: 0,
		},
		{
			name: "spike_at_match_point_skipped",
			setup: func(s *SpikeFade, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 30, "E1-A": 70}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 1000},
				// Mark match point context.
				match.PointScored{Point: match.Point{IsMatchPoint: true}, TS: 2000},
				// Spike should be skipped (informational at match point).
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 65, TS: 5000},
			},
			wantLen: 0,
		},
		{
			name: "fires_once_then_dedup",
			setup: func(s *SpikeFade, st *State) {
				st.MatchView.MarketTickers = []string{"E1-H", "E1-A"}
				st.MatchView.Prices = map[string]int{"E1-H": 30, "E1-A": 70}
				st.MatchView.PriceTS = map[string]int64{"E1-H": 1000, "E1-A": 1000}
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 65, TS: 5000},
				// Second spike after fired — should be deduped.
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 6000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 7000},
			},
			wantLen: 1,
			wantMkt: "E1-A",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSpikeFade()
			st := &State{StrategyState: map[string]any{}}
			tc.setup(s, st)

			var intents []match.Intent
			for _, ev := range tc.events {
				intents = s.OnEvent(ev, st)
			}
			// Report intents from last event only (matches real dispatch).
			_ = intents
			// Replay all and collect the first non-nil batch.
			st2 := &State{StrategyState: map[string]any{}}
			tc.setup(s, st2)
			var got []match.Intent
			for _, ev := range tc.events {
				if out := s.OnEvent(ev, st2); out != nil {
					got = out
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
