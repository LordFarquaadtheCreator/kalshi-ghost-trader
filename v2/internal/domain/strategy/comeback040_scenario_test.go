package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestComeback040Scenarios(t *testing.T) {
	// point040Home: home serving, down 0-40.
	point040Home := func() match.PointScored {
		return match.PointScored{
			EventTicker: "E1",
			Point: match.Point{
				SetNumber: 1, GameNumber: 4, PointNumber: 3,
				Server: 1, HomeGames: 2, AwayGames: 1,
				HomePoints: "0", AwayPoints: "40",
			},
			TS: 5000,
		}
	}

	// point040Away: away serving, down 0-40.
	point040Away := func() match.PointScored {
		return match.PointScored{
			EventTicker: "E1",
			Point: match.Point{
				SetNumber: 1, GameNumber: 5, PointNumber: 3,
				Server: 2, HomeGames: 2, AwayGames: 2,
				HomePoints: "40", AwayPoints: "0",
			},
			TS: 5000,
		}
	}

	// pointNot040: 30-40, not 0-40.
	pointNot040 := func() match.PointScored {
		return match.PointScored{
			EventTicker: "E1",
			Point: match.Point{
				SetNumber: 1, GameNumber: 4, PointNumber: 2,
				Server: 1, HomeGames: 2, AwayGames: 1,
				HomePoints: "30", AwayPoints: "40",
			},
			TS: 5000,
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
			name: "home_040_buy_home_market",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 30, "E1-A": 70},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 2000},
				point040Home(),
			},
			wantLen: 1,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
				if intents[0].MarketTicker != "E1-H" {
					t.Errorf("MarketTicker = %s, want E1-H", intents[0].MarketTicker)
				}
				if intents[0].ConvProbBps != 3500 {
					t.Errorf("ConvProbBps = %d, want 3500", intents[0].ConvProbBps)
				}
			},
		},
		{
			name: "away_040_buy_away_market",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 70, "E1-A": 30},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-A", PriceCents: 55, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-A", PriceCents: 30, TS: 2000},
				point040Away(),
			},
			wantLen: 1,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].MarketTicker != "E1-A" {
					t.Errorf("MarketTicker = %s, want E1-A", intents[0].MarketTicker)
				}
			},
		},
		{
			name: "not_040_no_fire",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 30, "E1-A": 70},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 1000},
				pointNot040(),
			},
			wantLen: 0,
		},
		{
			name: "peak_too_low_no_fire",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 30, "E1-A": 70},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 35, TS: 1000},
				point040Home(),
			},
			wantLen: 0,
		},
		{
			name: "price_too_high_no_fire",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 75, "E1-A": 25},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 80, TS: 1000},
				point040Home(),
			},
			wantLen: 0,
		},
		{
			name: "fires_once_second_ignored",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{"E1-H": 30, "E1-A": 70},
				PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 30, TS: 2000},
				point040Home(),
				point040Home(),
			},
			wantLen: 1,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewComeback040()
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
