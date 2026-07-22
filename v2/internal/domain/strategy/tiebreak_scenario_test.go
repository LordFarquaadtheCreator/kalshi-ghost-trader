package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestTiebreakScenarios(t *testing.T) {
	tests := []struct {
		name    string
		events  func() []match.Event
		wantLen int
		wantMkt string
	}{
		{
			name: "price_drop_in_band_fires",
			events: func() []match.Event {
				return []match.Event{
					// Peak at 50c (above min peak 40c).
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
					// Drop to 40c = 20% drop (in 10-25% band), entry in 25-60c.
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 2000},
				}
			},
			wantLen: 1,
			wantMkt: "E1-H",
		},
		{
			name: "drop_too_small_no_fire",
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
					// 5% drop — below 10% min.
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 48, TS: 2000},
				}
			},
			wantLen: 0,
		},
		{
			name: "minibreak_in_tiebreak_fires_on_server_market",
			events: func() []match.Event {
				return []match.Event{
					// Establish peak price for the server market.
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
					match.PriceUpdate{MarketTicker: "E1-A", PriceCents: 50, TS: 1000},
					// Mini-break: server=1 (home), scorer=2 (away). Buy home (broken).
					match.PointScored{
						Point: match.Point{
							SetNumber: 1, IsTiebreak: true,
							Server: 1, Scorer: 2,
							HomePoints: "0", AwayPoints: "1",
						},
						TS: 2000,
					},
				}
			},
			wantLen: 1,
			wantMkt: "E1-H",
		},
		{
			name: "no_minibreak_when_scorer_equals_server",
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
					match.PriceUpdate{MarketTicker: "E1-A", PriceCents: 50, TS: 1000},
					// Hold (scorer == server) — not a mini-break.
					match.PointScored{
						Point: match.Point{
							SetNumber: 1, IsTiebreak: true,
							Server: 1, Scorer: 1,
							HomePoints: "1", AwayPoints: "0",
						},
						TS: 2000,
					},
				}
			},
			wantLen: 0,
		},
		{
			name: "fires_once_then_dedup",
			events: func() []match.Event {
				return []match.Event{
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 2000},
					// Second qualifying drop — deduped.
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 50, TS: 3000},
					match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 4000},
				}
			},
			wantLen: 1,
			wantMkt: "E1-H",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewTiebreak()
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{},
					PriceTS:       map[string]int64{},
				},
				StrategyState: map[string]any{},
			}

			var got []match.Intent
			for _, ev := range tc.events() {
				if out := s.OnEvent(ev, st); out != nil {
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
