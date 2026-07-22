package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// TestCloseTimerScenarios — table-driven tests covering the close-timer
// strategy decision logic: favorite buy, below-threshold skip, stale-price
// skip, dedup, outside-window skip, final-window skip.
func TestCloseTimerScenarios(t *testing.T) {
	tests := []struct {
		name       string
		closeTS    int64
		tickTS     int64
		homePrice  int
		awayPrice  int
		homePriceTS int64
		awayPriceTS int64
		preFire    bool // fire once before the real tick to test dedup
		wantIntents int
		wantTicker  string
	}{
		{
			name:        "favorite_above_threshold_within_window_buys",
			closeTS:     10 * 60 * 1000, // 10 min from t=0
			tickTS:      0,
			homePrice:   90,
			awayPrice:   10,
			homePriceTS: 0,
			awayPriceTS: 0,
			wantIntents: 1,
			wantTicker:  "E1-H",
		},
		{
			name:        "favorite_below_threshold_skips",
			closeTS:     10 * 60 * 1000,
			tickTS:      0,
			homePrice:   50,
			awayPrice:   40,
			homePriceTS: 0,
			awayPriceTS: 0,
			wantIntents: 0,
		},
		{
			name:        "stale_price_skips",
			closeTS:     10 * 60 * 1000,
			tickTS:      70_000, // 70s after price timestamp
			homePrice:   90,
			awayPrice:   10,
			homePriceTS: 0,
			awayPriceTS: 0,
			wantIntents: 0,
		},
		{
			name:        "dedup_one_order_per_close_window",
			closeTS:     10 * 60 * 1000,
			tickTS:      0,
			homePrice:   90,
			awayPrice:   10,
			homePriceTS: 0,
			awayPriceTS: 0,
			preFire:     true,
			wantIntents: 0,
		},
		{
			name:        "outside_lead_window_skips",
			closeTS:     20 * 60 * 1000, // 20 min away
			tickTS:      0,
			homePrice:   90,
			awayPrice:   10,
			homePriceTS: 0,
			awayPriceTS: 0,
			wantIntents: 0,
		},
		{
			name:        "final_60s_window_skips",
			closeTS:     30 * 1000, // 30s to close
			tickTS:      0,
			homePrice:   90,
			awayPrice:   10,
			homePriceTS: 0,
			awayPriceTS: 0,
			wantIntents: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCloseTimer()
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					CloseTS:       tc.closeTS,
					Prices: map[string]int{
						"E1-H": tc.homePrice,
						"E1-A": tc.awayPrice,
					},
					PriceTS: map[string]int64{
						"E1-H": tc.homePriceTS,
						"E1-A": tc.awayPriceTS,
					},
				},
				StrategyState: map[string]any{},
			}

			if tc.preFire {
				s.OnEvent(match.ClockTick{TS: tc.tickTS}, st)
			}

			intents := s.OnEvent(match.ClockTick{TS: tc.tickTS}, st)

			if len(intents) != tc.wantIntents {
				t.Fatalf("got %d intents, want %d", len(intents), tc.wantIntents)
			}
			if tc.wantIntents == 1 && intents[0].MarketTicker != tc.wantTicker {
				t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tc.wantTicker)
			}
			if tc.wantIntents == 1 {
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
				if intents[0].ConvProbBps != ctConvProbBps {
					t.Errorf("ConvProbBps = %d, want %d", intents[0].ConvProbBps, ctConvProbBps)
				}
				if intents[0].Strategy != "close_timer" {
					t.Errorf("Strategy = %s, want close_timer", intents[0].Strategy)
				}
			}
		})
	}
}

// TestCloseTimerLifecycleResetsFired: settled event resets fired state so a
// close_ts extension re-evaluates.
func TestCloseTimerLifecycleResetsFired(t *testing.T) {
	s := NewCloseTimer()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			CloseTS:       10 * 60 * 1000,
			Prices:        map[string]int{"E1-H": 90, "E1-A": 10},
			PriceTS:       map[string]int64{"E1-H": 0, "E1-A": 0},
		},
		StrategyState: map[string]any{},
	}

	// first tick fires
	intents := s.OnEvent(match.ClockTick{TS: 0}, st)
	if len(intents) != 1 {
		t.Fatalf("first tick: got %d intents, want 1", len(intents))
	}

	// second tick deduped
	intents = s.OnEvent(match.ClockTick{TS: 1000}, st)
	if len(intents) != 0 {
		t.Fatalf("second tick: got %d intents, want 0 (deduped)", len(intents))
	}

	// settled resets
	s.OnEvent(match.LifecycleChange{Type: "settled", TS: 2000}, st)

	// third tick fires again after reset
	intents = s.OnEvent(match.ClockTick{TS: 3000}, st)
	if len(intents) != 1 {
		t.Fatalf("after reset: got %d intents, want 1", len(intents))
	}
}
