package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestCalibratedMarkovScenarios(t *testing.T) {
	// dominatingPoint: home at 5-0, 40-0, home serving — fair value very high.
	dominatingPoint := func() match.PointScored {
		return match.PointScored{
			EventTicker: "E1",
			Point: match.Point{
				SetNumber: 1, GameNumber: 6, PointNumber: 1,
				Server: 1, HomeGames: 5, AwayGames: 0,
				HomePoints: "40", AwayPoints: "0",
			},
			TS: 1000,
		}
	}

	// startOfMatch: 0-0, 0-0, home serving — fair value ~0.55.
	startPoint := func() match.PointScored {
		return match.PointScored{
			EventTicker: "E1",
			Point: match.Point{
				SetNumber: 1, GameNumber: 1, PointNumber: 1,
				Server: 1, HomeGames: 0, AwayGames: 0,
				HomePoints: "0", AwayPoints: "0",
			},
			TS: 1000,
		}
	}

	tests := []struct {
		name    string
		mv      MatchView
		events  []match.Event
		wantLen int
		check   func(t *testing.T, intents []match.Intent)
	}{
		{
			name: "edge_fires_home_dominating",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 30, "E1-A": 70},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				dominatingPoint(),
			},
			wantLen: 1,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
				if intents[0].MarketTicker != "E1-H" {
					t.Errorf("MarketTicker = %s, want E1-H", intents[0].MarketTicker)
				}
				if intents[0].ConvProbBps <= 0 {
					t.Errorf("ConvProbBps = %d, want > 0", intents[0].ConvProbBps)
				}
			},
		},
		{
			name: "price_too_high_no_fire",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 90, "E1-A": 90},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				dominatingPoint(),
			},
			wantLen: 0,
		},
		{
			name: "no_edge_start_of_match",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 85, "E1-A": 85},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				startPoint(),
			},
			wantLen: 0,
		},
		{
			name: "fires_once_second_point_ignored",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 30, "E1-A": 70},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				dominatingPoint(),
				dominatingPoint(),
			},
			wantLen: 1,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
			},
		},
		{
			name: "no_prices_no_fire",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				dominatingPoint(),
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewCalibratedMarkov("") // no model file — uses default pServe
			st := &State{
				MatchView:     tt.mv,
				StrategyState: map[string]any{},
			}
			var allIntents []match.Intent
			for _, ev := range tt.events {
				intents := s.OnEvent(ev, st)
				allIntents = append(allIntents, intents...)
			}
			if len(allIntents) != tt.wantLen {
				t.Fatalf("got %d intents, want %d", len(allIntents), tt.wantLen)
			}
			if tt.check != nil && len(allIntents) > 0 {
				tt.check(t, allIntents)
			}
		})
	}
}
