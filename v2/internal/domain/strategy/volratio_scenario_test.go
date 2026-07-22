package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestVolumeRatioScenarios(t *testing.T) {
	// closeTs = 1_000_000 ms. WindowSeconds = 600 → entryTs = 940_000.
	const closeTS int64 = 1_000_000

	tests := []struct {
		name     string
		volHome  float64
		volAway  float64
		homePx   int
		awayPx   int
		priceTS  int64
		wantLen  int
		wantMkt  string
	}{
		{
			name:    "heavy_home_volume_fires",
			volHome: 5000,
			volAway: 2000,
			homePx:  40,
			awayPx:  60,
			priceTS: 950_000,
			wantLen: 1,
			wantMkt: "E1-H",
		},
		{
			name:    "ratio_below_threshold_no_fire",
			volHome: 3000,
			volAway: 2000,
			homePx:  40,
			awayPx:  60,
			priceTS: 950_000,
			wantLen: 0,
		},
		{
			name:    "before_entry_window_no_fire",
			volHome: 5000,
			volAway: 2000,
			homePx:  40,
			awayPx:  60,
			priceTS: 300_000, // before T-600s (entryTs=400_000)
			wantLen: 0,
		},
		{
			name:    "heavy_away_volume_fires_away",
			volHome: 1000,
			volAway: 5000,
			homePx:  70,
			awayPx:  30,
			priceTS: 950_000,
			wantLen: 1,
			wantMkt: "E1-A",
		},
		{
			name:    "price_out_of_range_no_fire",
			volHome: 5000,
			volAway: 2000,
			homePx:  5, // below min 10c
			awayPx:  95,
			priceTS: 950_000,
			wantLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewVolumeRatio()
			s.Volumes = map[string]float64{"E1-H": tc.volHome, "E1-A": tc.volAway}
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					OccurrenceTS:  closeTS,
					Prices:        map[string]int{"E1-H": tc.homePx, "E1-A": tc.awayPx},
					PriceTS:       map[string]int64{"E1-H": tc.priceTS, "E1-A": tc.priceTS},
				},
				StrategyState: map[string]any{},
			}

			// Price update for home market at priceTS.
			ev := match.PriceUpdate{MarketTicker: "E1-H", PriceCents: tc.homePx, TS: tc.priceTS}
			got := s.OnEvent(ev, st)

			if len(got) != tc.wantLen {
				t.Fatalf("got %d intents, want %d", len(got), tc.wantLen)
			}
			if tc.wantLen > 0 && got[0].MarketTicker != tc.wantMkt {
				t.Errorf("MarketTicker = %s, want %s", got[0].MarketTicker, tc.wantMkt)
			}
		})
	}
}

func TestVolumeRatioDedup(t *testing.T) {
	s := NewVolumeRatio()
	s.Volumes = map[string]float64{"E1-H": 5000, "E1-A": 2000}
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			OccurrenceTS:  1_000_000,
			Prices:        map[string]int{"E1-H": 40, "E1-A": 60},
			PriceTS:       map[string]int64{"E1-H": 950_000, "E1-A": 950_000},
		},
		StrategyState: map[string]any{},
	}

	ev := match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 950_000}
	got1 := s.OnEvent(ev, st)
	if len(got1) != 1 {
		t.Fatalf("first: got %d intents, want 1", len(got1))
	}
	// Second price update — deduped.
	got2 := s.OnEvent(ev, st)
	if len(got2) != 0 {
		t.Fatalf("second: got %d intents, want 0 (dedup)", len(got2))
	}
}
