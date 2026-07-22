package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestSet1WinnerScenarios(t *testing.T) {
	tests := []struct {
		name       string
		homeSet1   int
		awaySet1   int
		priceHome   int
		priceAway   int
		priceTSH    int64
		priceTSA    int64
		wantIntents int
		wantMkt     string
		wantReason  string
	}{
		{
			name:        "home_won_set1_price_under_72_buys_home",
			homeSet1:    6, awaySet1: 4,
			priceHome:   60, priceAway: 40,
			priceTSH:    1000, priceTSA: 1000,
			wantIntents: 1, wantMkt: "E1-H", wantReason: "set1winner_s2start",
		},
		{
			name:        "away_won_set1_price_under_72_buys_away",
			homeSet1:    4, awaySet1: 6,
			priceHome:   40, priceAway: 65,
			priceTSH:    1000, priceTSA: 1000,
			wantIntents: 1, wantMkt: "E1-A", wantReason: "set1winner_s2start",
		},
		{
			name:        "price_too_high_no_intent",
			homeSet1:    6, awaySet1: 4,
			priceHome:   75, priceAway: 25,
			priceTSH:    1000, priceTSA: 1000,
			wantIntents: 0,
		},
		{
			name:        "edge_too_small_no_intent",
			homeSet1:    6, awaySet1: 4,
			priceHome:   70, priceAway: 30,
			priceTSH:    1000, priceTSA: 1000,
			wantIntents: 0, // edge = 72-70 = 2 < 5
		},
		{
			name:        "already_fired_no_second_intent",
			homeSet1:    6, awaySet1: 4,
			priceHome:   60, priceAway: 40,
			priceTSH:    1000, priceTSA: 1000,
			wantIntents: 1, wantMkt: "E1-H", wantReason: "set1winner_s2start",
		},
	}

	s := NewSet1Winner()
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{"E1-H": tt.priceHome, "E1-A": tt.priceAway},
					PriceTS:       map[string]int64{"E1-H": tt.priceTSH, "E1-A": tt.priceTSA},
				},
				StrategyState: map[string]any{},
			}

			p := match.PointScored{
				EventTicker: "E1",
				Point: match.Point{
					SetNumber: 2, GameNumber: 1, PointNumber: 1,
					HomeSetGames: tt.homeSet1, AwaySetGames: tt.awaySet1,
					Server: 1, Scorer: 1,
				},
				TS: 2000,
			}
			intents := s.OnEvent(p, st)

			// For the "already fired" case, send a second point.
			if tt.name == "already_fired_no_second_intent" {
				if len(intents) != 1 {
					t.Fatalf("first fire: got %d intents, want 1", len(intents))
				}
				p2 := match.PointScored{
					EventTicker: "E1",
					Point: match.Point{
						SetNumber: 2, GameNumber: 1, PointNumber: 2,
						HomeSetGames: tt.homeSet1, AwaySetGames: tt.awaySet1,
						Server: 1, Scorer: 1,
					},
					TS: 2100,
				}
				intents = s.OnEvent(p2, st)
				if len(intents) != 0 {
					t.Fatalf("second fire: got %d intents, want 0 (already fired)", len(intents))
				}
				return
			}

			if len(intents) != tt.wantIntents {
				t.Fatalf("test %d: got %d intents, want %d", i, len(intents), tt.wantIntents)
			}
			if tt.wantIntents == 1 {
				if intents[0].MarketTicker != tt.wantMkt {
					t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tt.wantMkt)
				}
				if intents[0].Reason != tt.wantReason {
					t.Errorf("Reason = %s, want %s", intents[0].Reason, tt.wantReason)
				}
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
				if intents[0].ConvProbBps != 7200 {
					t.Errorf("ConvProbBps = %d, want 7200", intents[0].ConvProbBps)
				}
			}
		})
	}
}

// TestSet1WinnerWrongPoint: non-set-2-start point → no intent.
func TestSet1WinnerWrongPoint(t *testing.T) {
	s := NewSet1Winner()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 60, "E1-A": 40},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	// Set 1 point, not set 2 start.
	p := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 1, GameNumber: 5, PointNumber: 1,
			HomeSetGames: 0, AwaySetGames: 0,
		},
		TS: 1000,
	}
	intents := s.OnEvent(p, st)
	if len(intents) != 0 {
		t.Fatalf("got %d intents, want 0 (not set 2 start)", len(intents))
	}
}
