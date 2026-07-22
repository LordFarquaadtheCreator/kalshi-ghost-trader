package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestSetDownPriceDropScenarios(t *testing.T) {
	tests := []struct {
		name        string
		prices      []match.PriceUpdate // sequential price updates
		wantIntents int
		wantMkt     string
	}{
		{
			name: "favourite_drops_into_entry_range_buys",
			prices: []match.PriceUpdate{
				{MarketTicker: "E1-H", PriceCents: 60, TS: 1000}, // peak
				{MarketTicker: "E1-H", PriceCents: 40, TS: 2000}, // dropped into range
			},
			wantIntents: 1, wantMkt: "E1-H",
		},
		{
			name: "never_was_favourite_no_intent",
			prices: []match.PriceUpdate{
				{MarketTicker: "E1-H", PriceCents: 50, TS: 1000},
				{MarketTicker: "E1-H", PriceCents: 40, TS: 2000},
			},
			wantIntents: 0,
		},
		{
			name: "price_too_low_no_intent",
			prices: []match.PriceUpdate{
				{MarketTicker: "E1-H", PriceCents: 60, TS: 1000},
				{MarketTicker: "E1-H", PriceCents: 25, TS: 2000}, // below min entry
			},
			wantIntents: 0,
		},
		{
			name: "drop_too_small_no_intent",
			prices: []match.PriceUpdate{
				{MarketTicker: "E1-H", PriceCents: 56, TS: 1000},
				{MarketTicker: "E1-H", PriceCents: 48, TS: 2000}, // drop=8 < 10
			},
			wantIntents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSetDown()
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{},
					PriceTS:       map[string]int64{},
				},
				StrategyState: map[string]any{},
			}

			var intents []match.Intent
			for _, pu := range tt.prices {
				intents = s.OnEvent(pu, st)
			}

			if len(intents) != tt.wantIntents {
				t.Fatalf("got %d intents, want %d", len(intents), tt.wantIntents)
			}
			if tt.wantIntents == 1 && intents[0].MarketTicker != tt.wantMkt {
				t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tt.wantMkt)
			}
			if tt.wantIntents == 1 && intents[0].Action != "buy" {
				t.Errorf("Action = %s, want buy", intents[0].Action)
			}
			if tt.wantIntents == 1 && intents[0].ConvProbBps != 5500 {
				t.Errorf("ConvProbBps = %d, want 5500", intents[0].ConvProbBps)
			}
		})
	}
}

func TestSetDownScoreBasedScenarios(t *testing.T) {
	tests := []struct {
		name        string
		peakPrice   int // price update to establish favourite status
		curPrice    int // current price at set boundary
		homeSet1    int
		awaySet1    int
		wantIntents int
		wantMkt     string
	}{
		{
			name:        "home_lost_set1_was_favourite_buys_home",
			peakPrice:   60, curPrice: 40,
			homeSet1:    3, awaySet1: 6, // home lost
			wantIntents: 1, wantMkt: "E1-H",
		},
		{
			name:        "away_lost_set1_was_favourite_buys_away",
			peakPrice:   60, curPrice: 35,
			homeSet1:    6, awaySet1: 3, // away lost
			wantIntents: 1, wantMkt: "E1-A",
		},
		{
			name:        "loser_not_favourite_no_intent",
			peakPrice:   50, curPrice: 40,
			homeSet1:    3, awaySet1: 6,
			wantIntents: 0,
		},
		{
			name:        "price_outside_entry_range_no_intent",
			peakPrice:   60, curPrice: 50,
			homeSet1:    3, awaySet1: 6,
			wantIntents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSetDown()
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{"E1-H": tt.curPrice, "E1-A": tt.curPrice},
					PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
				},
				StrategyState: map[string]any{},
			}

			// Establish peak price on both markets.
			s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: tt.peakPrice, TS: 500}, st)
			s.OnEvent(match.PriceUpdate{MarketTicker: "E1-A", PriceCents: tt.peakPrice, TS: 500}, st)

			// Set boundary point.
			p := match.PointScored{
				EventTicker: "E1",
				Point: match.Point{
					SetNumber: 2, GameNumber: 1, PointNumber: 1,
					HomeSetGames: tt.homeSet1, AwaySetGames: tt.awaySet1,
				},
				TS: 2000,
			}
			intents := s.OnEvent(p, st)

			if len(intents) != tt.wantIntents {
				t.Fatalf("got %d intents, want %d", len(intents), tt.wantIntents)
			}
			if tt.wantIntents == 1 && intents[0].MarketTicker != tt.wantMkt {
				t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tt.wantMkt)
			}
		})
	}
}

// TestSetDownFiresOnce: after firing on price path, score path does not re-fire.
func TestSetDownFiresOnce(t *testing.T) {
	s := NewSetDown()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 40, "E1-A": 40},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	// Price path fires.
	s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 500}, st)
	intents := s.OnEvent(match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 1000}, st)
	if len(intents) != 1 {
		t.Fatalf("price path: got %d intents, want 1", len(intents))
	}

	// Score path should not re-fire.
	p := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 2, GameNumber: 1, PointNumber: 1,
			HomeSetGames: 3, AwaySetGames: 6,
		},
		TS: 2000,
	}
	intents = s.OnEvent(p, st)
	if len(intents) != 0 {
		t.Fatalf("score path after fire: got %d intents, want 0", len(intents))
	}
}
