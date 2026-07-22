package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestTiebreakServerScenarios(t *testing.T) {
	tests := []struct {
		name    string
		server  int
		homePx  int
		awayPx  int
		point   match.Point
		wantLen int
		wantMkt string
	}{
		{
			name:    "home_serving_tiebreak_start_fires",
			server:  1,
			homePx:  50,
			awayPx:  50,
			point:   match.Point{SetNumber: 1, PointNumber: 1, IsTiebreak: true, Server: 1},
			wantLen: 1,
			wantMkt: "E1-H",
		},
		{
			name:    "away_serving_tiebreak_start_fires",
			server:  2,
			homePx:  50,
			awayPx:  50,
			point:   match.Point{SetNumber: 1, PointNumber: 1, IsTiebreak: true, Server: 2},
			wantLen: 1,
			wantMkt: "E1-A",
		},
		{
			name:    "not_tiebreak_no_fire",
			server:  1,
			homePx:  50,
			awayPx:  50,
			point:   match.Point{SetNumber: 1, PointNumber: 1, IsTiebreak: false, Server: 1},
			wantLen: 0,
		},
		{
			name:    "price_too_high_no_edge",
			server:  1,
			homePx:  60,
			awayPx:  40,
			// 60c price, convProb 60c → edge 0 < min 5c.
			point:   match.Point{SetNumber: 1, PointNumber: 1, IsTiebreak: true, Server: 1},
			wantLen: 0,
		},
		{
			name:    "not_first_point_no_fire",
			server:  1,
			homePx:  50,
			awayPx:  50,
			point:   match.Point{SetNumber: 1, PointNumber: 2, IsTiebreak: true, Server: 1},
			wantLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewTiebreakServer()
			st := &State{
				MatchView: MatchView{
					EventTicker:   "E1",
					MarketTickers: []string{"E1-H", "E1-A"},
					Prices:        map[string]int{"E1-H": tc.homePx, "E1-A": tc.awayPx},
					PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
				},
				StrategyState: map[string]any{},
			}

			ev := match.PointScored{EventTicker: "E1", Point: tc.point, TS: 1000}
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

func TestTiebreakServerDedup(t *testing.T) {
	s := NewTiebreakServer()
	st := &State{
		MatchView: MatchView{
			EventTicker:   "E1",
			MarketTickers: []string{"E1-H", "E1-A"},
			Prices:        map[string]int{"E1-H": 50, "E1-A": 50},
			PriceTS:       map[string]int64{"E1-H": 1000, "E1-A": 1000},
		},
		StrategyState: map[string]any{},
	}

	p := match.PointScored{
		EventTicker: "E1",
		Point:       match.Point{SetNumber: 1, PointNumber: 1, IsTiebreak: true, Server: 1},
		TS:          1000,
	}
	got1 := s.OnEvent(p, st)
	if len(got1) != 1 {
		t.Fatalf("first: got %d intents, want 1", len(got1))
	}
	// Second tiebreak point — deduped.
	p2 := p
	p2.Point.PointNumber = 2
	got2 := s.OnEvent(p2, st)
	if len(got2) != 0 {
		t.Fatalf("second: got %d intents, want 0 (dedup)", len(got2))
	}
}
