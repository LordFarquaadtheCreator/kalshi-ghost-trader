package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestSetPointScenarios(t *testing.T) {
	tests := []struct {
		name        string
		// seed point to establish set tracking
		seedSet     int
		seedGame    int
		seedHome    int
		seedAway    int
		seedScorer  int
		// trigger point
		trigSet     int
		trigGame    int
		trigHome    int
		trigAway    int
		trigServer  int
		homePoints  string
		awayPoints  string
		priceHome   int
		priceAway   int
		wantIntents int
		wantMkt     string
		wantReason  string
		wantConvBps int
	}{
		{
			name:        "home_set_point_serving_buys",
			seedSet:     1, seedGame: 8, seedHome: 6, seedAway: 4, seedScorer: 1,
			trigSet:     1, trigGame: 9, trigHome: 5, trigAway: 3, trigServer: 1,
			homePoints:  "40", awayPoints: "30",
			priceHome:   50, priceAway: 50,
			wantIntents: 1, wantMkt: "E1-H", wantReason: "home_set_point", wantConvBps: 9300,
		},
		{
			name:        "home_set_point_returning_buys",
			seedSet:     1, seedGame: 8, seedHome: 6, seedAway: 4, seedScorer: 1,
			trigSet:     1, trigGame: 9, trigHome: 5, trigAway: 3, trigServer: 2,
			homePoints:  "40", awayPoints: "30",
			priceHome:   50, priceAway: 50,
			wantIntents: 1, wantMkt: "E1-H", wantReason: "home_set_point", wantConvBps: 8900,
		},
		{
			name:        "home_match_point_serving_buys",
			seedSet:     1, seedGame: 8, seedHome: 6, seedAway: 4, seedScorer: 1,
			trigSet:     2, trigGame: 9, trigHome: 5, trigAway: 3, trigServer: 1,
			homePoints:  "40", awayPoints: "30",
			priceHome:   50, priceAway: 50,
			wantIntents: 1, wantMkt: "E1-H", wantReason: "home_match_point", wantConvBps: 9300,
		},
		{
			name:        "no_set_point_game_not_far_enough",
			seedSet:     1, seedGame: 5, seedHome: 4, seedAway: 3, seedScorer: 1,
			trigSet:     1, trigGame: 6, trigHome: 4, trigAway: 3, trigServer: 1,
			homePoints:  "40", awayPoints: "30",
			priceHome:   50, priceAway: 50,
			wantIntents: 0, // 4 < 5 (gamesPerSet-1)
		},
		{
			name:        "stale_price_no_intent",
			seedSet:     1, seedGame: 8, seedHome: 6, seedAway: 4, seedScorer: 1,
			trigSet:     1, trigGame: 9, trigHome: 5, trigAway: 3, trigServer: 1,
			homePoints:  "40", awayPoints: "30",
			priceHome:   50, priceAway: 50,
			wantIntents: 0, // price TS stale — handled via PriceTS below
		},
		{
			name:        "tiebreak_no_intent",
			seedSet:     1, seedGame: 12, seedHome: 6, seedAway: 6, seedScorer: 1,
			trigSet:     1, trigGame: 13, trigHome: 6, trigAway: 6, trigServer: 1,
			homePoints:  "40", awayPoints: "30",
			priceHome:   50, priceAway: 50,
			wantIntents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSetPoint()
			priceTS := int64(1000)
			eventTS := int64(2000)
			if tt.name == "stale_price_no_intent" {
				priceTS = 1000
				eventTS = 70000 // > 60s stale
			}
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{"E1-H": tt.priceHome, "E1-A": tt.priceAway},
					PriceTS:       map[string]int64{"E1-H": priceTS, "E1-A": priceTS},
				},
				StrategyState: map[string]any{},
			}

			// Seed point to establish set tracking.
			seed := match.PointScored{
				EventTicker: "E1",
				Point: match.Point{
					SetNumber: tt.seedSet, GameNumber: tt.seedGame, PointNumber: 1,
					HomeGames: tt.seedHome, AwayGames: tt.seedAway, Scorer: tt.seedScorer,
					Server: 1, HomePoints: "0", AwayPoints: "0",
				},
				TS: 1000,
			}
			s.OnEvent(seed, st)

			// Trigger point.
			isTB := tt.name == "tiebreak_no_intent"
			trig := match.PointScored{
				EventTicker: "E1",
				Point: match.Point{
					SetNumber: tt.trigSet, GameNumber: tt.trigGame, PointNumber: 1,
					HomeGames: tt.trigHome, AwayGames: tt.trigAway, Server: tt.trigServer,
					HomePoints: tt.homePoints, AwayPoints: tt.awayPoints,
					IsTiebreak: isTB,
				},
				TS: eventTS,
			}
			intents := s.OnEvent(trig, st)

			if len(intents) != tt.wantIntents {
				t.Fatalf("got %d intents, want %d", len(intents), tt.wantIntents)
			}
			if tt.wantIntents == 1 {
				if intents[0].MarketTicker != tt.wantMkt {
					t.Errorf("MarketTicker = %s, want %s", intents[0].MarketTicker, tt.wantMkt)
				}
				if intents[0].Reason != tt.wantReason {
					t.Errorf("Reason = %s, want %s", intents[0].Reason, tt.wantReason)
				}
				if intents[0].ConvProbBps != tt.wantConvBps {
					t.Errorf("ConvProbBps = %d, want %d", intents[0].ConvProbBps, tt.wantConvBps)
				}
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
			}
		})
	}
}

// TestSetPointDedup: same point twice → only one intent.
func TestSetPointDedup(t *testing.T) {
	s := NewSetPoint()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 50, "E1-A": 50},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	seed := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 1, GameNumber: 8, PointNumber: 1,
			HomeGames: 6, AwayGames: 4, Scorer: 1, Server: 1,
		},
		TS: 1000,
	}
	s.OnEvent(seed, st)

	trig := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 1, GameNumber: 9, PointNumber: 1,
			HomeGames: 5, AwayGames: 3, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 2000,
	}
	intents := s.OnEvent(trig, st)
	if len(intents) != 1 {
		t.Fatalf("first: got %d intents, want 1", len(intents))
	}
	// Same point again — dedup.
	intents = s.OnEvent(trig, st)
	if len(intents) != 0 {
		t.Fatalf("dup: got %d intents, want 0 (dedup)", len(intents))
	}
}
