package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// TestMatchPointHomeMatchPointServing: home at set point, serving, match point → buy intent.
func TestMatchPointHomeMatchPointServing(t *testing.T) {
	s := NewMatchPoint()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 50, "E1-A": 50},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	// Simulate: home won set 1, now in set 2 at 5-3, 40-30, home serving.
	// First point to establish set tracking.
	p1 := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 1, GameNumber: 8, PointNumber: 1,
			HomeGames: 6, AwayGames: 4, Scorer: 1, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 1000,
	}
	s.OnEvent(p1, st)

	// Set 2, game 9, home serving at 40-30, 5-3 in set.
	// Home needs 1 more set (won set 1), home can win game, 5 >= 5, 5 > 3.
	p2 := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 2, GameNumber: 9, PointNumber: 1,
			HomeGames: 5, AwayGames: 3, Scorer: 1, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 2000,
	}
	intents := s.OnEvent(p2, st)

	if len(intents) != 1 {
		t.Fatalf("got %d intents, want 1", len(intents))
	}
	if intents[0].MarketTicker != "E1-H" {
		t.Errorf("MarketTicker = %s, want E1-H", intents[0].MarketTicker)
	}
	if intents[0].Action != "buy" {
		t.Errorf("Action = %s, want buy", intents[0].Action)
	}
	if intents[0].Reason != "home_match_point" {
		t.Errorf("Reason = %s, want home_match_point", intents[0].Reason)
	}
}

// TestMatchPointNoMatchPointWhenReturning: match point but returner serving → no intent.
func TestMatchPointNoMatchPointWhenReturning(t *testing.T) {
	s := NewMatchPoint()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 50, "E1-A": 50},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	// Set 1 to establish tracking.
	p1 := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 1, GameNumber: 8, PointNumber: 1,
			HomeGames: 6, AwayGames: 4, Scorer: 1, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 1000,
	}
	s.OnEvent(p1, st)

	// Set 2, home at match point but AWAY is serving (server=2).
	p2 := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 2, GameNumber: 9, PointNumber: 1,
			HomeGames: 5, AwayGames: 3, Scorer: 1, Server: 2,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 2000,
	}
	intents := s.OnEvent(p2, st)

	if len(intents) != 0 {
		t.Fatalf("got %d intents, want 0 (returner serving)", len(intents))
	}
}

// TestMatchPointStalePrice: price too old → no intent.
func TestMatchPointStalePrice(t *testing.T) {
	s := NewMatchPoint()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 50, "E1-A": 50},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	p1 := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 1, GameNumber: 8, PointNumber: 1,
			HomeGames: 6, AwayGames: 4, Scorer: 1, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 1000,
	}
	s.OnEvent(p1, st)

	// Price timestamp is 1000, event TS is 70000 — stale (>60s).
	p2 := match.PointScored{
		EventTicker: "E1",
		Point: match.Point{
			SetNumber: 2, GameNumber: 9, PointNumber: 1,
			HomeGames: 5, AwayGames: 3, Scorer: 1, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		},
		TS: 70000,
	}
	intents := s.OnEvent(p2, st)

	if len(intents) != 0 {
		t.Fatalf("got %d intents, want 0 (stale price)", len(intents))
	}
}
