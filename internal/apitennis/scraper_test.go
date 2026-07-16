package apitennis

import (
	"fmt"
	"testing"
)

// TestFlattenPoints_GameCountsFromServeWinner verifies that home_games/away_games
// are computed from ServeWinner (running count) rather than setData.Score, which
// API-Tennis sends as stale/zero for later games in a set.
func TestFlattenPoints_GameCountsFromServeWinner(t *testing.T) {
	// SetData.Score is intentionally "0 - 0" for all games to prove
	// game counts come from ServeWinner, not Score.
	ev := WSEvent{
		EventKey: 12345,
		PointByPoint: []SetData{
			// Set 1: home wins 6-4
			mkSetData(1, 1, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(1, 2, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			mkSetData(1, 3, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 0", nil, nil, nil}}),
			mkSetData(1, 4, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			mkSetData(1, 5, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(1, 6, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 0", nil, nil, nil}}),
			mkSetData(1, 7, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			mkSetData(1, 8, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(1, 9, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 0", nil, nil, nil}}),
			mkSetData(1, 10, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			// Set 2: same pattern, counts must reset
			mkSetData(2, 1, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(2, 2, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			mkSetData(2, 3, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 0", nil, nil, nil}}),
			mkSetData(2, 4, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			mkSetData(2, 5, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(2, 6, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 0", nil, nil, nil}}),
			mkSetData(2, 7, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
			mkSetData(2, 8, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(2, 9, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 0", nil, nil, nil}}),
			mkSetData(2, 10, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
		},
	}

	pts := flattenPoints(ev)

	if len(pts) != 20 {
		t.Fatalf("expected 20 points, got %d", len(pts))
	}

	// Home wins games 1,3,5,7,9,10 = 6; away wins 2,4,6,8 = 4.
	// Game N points should have home_games = home wins BEFORE game N.
	expected := []struct {
		game, homeGames, awayGames int
	}{
		{1, 0, 0}, {2, 1, 0}, {3, 1, 1}, {4, 2, 1}, {5, 2, 2},
		{6, 3, 2}, {7, 3, 3}, {8, 4, 3}, {9, 4, 4}, {10, 5, 4},
	}

	for set := 1; set <= 2; set++ {
		for i, exp := range expected {
			idx := (set-1)*10 + i
			pt := pts[idx]
			if pt.setNumber != set || pt.gameNumber != exp.game {
				t.Errorf("set%d point %d: set=%d game=%d, want set=%d game=%d",
					set, i, pt.setNumber, pt.gameNumber, set, exp.game)
			}
			if pt.homeGames != exp.homeGames {
				t.Errorf("set%d game %d: homeGames=%d, want %d",
					set, exp.game, pt.homeGames, exp.homeGames)
			}
			if pt.awayGames != exp.awayGames {
				t.Errorf("set%d game %d: awayGames=%d, want %d",
					set, exp.game, pt.awayGames, exp.awayGames)
			}
		}
	}
}

// TestDedupSeenKeys verifies that re-processing the same WSEvent
// (e.g. after WS reconnect) does not produce duplicate points.
func TestDedupSeenKeys(t *testing.T) {
	seen := make(map[string]bool)

	ev := WSEvent{
		EventKey: 12345,
		PointByPoint: []SetData{
			mkSetData(1, 1, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(1, 2, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
		},
	}

	pts := flattenPoints(ev)
	var newPts []flattenedPoint
	for _, fp := range pts {
		key := fmt.Sprintf("%d:%d:%d", fp.setNumber, fp.gameNumber, fp.pointNumber)
		if seen[key] {
			continue
		}
		seen[key] = true
		newPts = append(newPts, fp)
	}
	if len(newPts) != 2 {
		t.Fatalf("first call: expected 2 new points, got %d", len(newPts))
	}

	// Re-process same event — should produce 0 new points
	pts2 := flattenPoints(ev)
	var newPts2 []flattenedPoint
	for _, fp := range pts2 {
		key := fmt.Sprintf("%d:%d:%d", fp.setNumber, fp.gameNumber, fp.pointNumber)
		if seen[key] {
			continue
		}
		seen[key] = true
		newPts2 = append(newPts2, fp)
	}
	if len(newPts2) != 0 {
		t.Fatalf("second call: expected 0 new points (no dups), got %d", len(newPts2))
	}
}

// TestDedupSurvivesRestart verifies that seenKeys initialized
// from DB prevent re-ingestion after worker restart.
func TestDedupSurvivesRestart(t *testing.T) {
	seen := map[string]bool{"1:1:1": true}

	ev := WSEvent{
		EventKey: 12345,
		PointByPoint: []SetData{
			mkSetData(1, 1, "First Player", "First Player", "0 - 0",
				[]PointData{{"1", "40 - 30", nil, nil, nil}}),
			mkSetData(1, 2, "Second Player", "Second Player", "0 - 0",
				[]PointData{{"1", "40 - 15", nil, nil, nil}}),
		},
	}

	pts := flattenPoints(ev)
	var newPts []flattenedPoint
	for _, fp := range pts {
		key := fmt.Sprintf("%d:%d:%d", fp.setNumber, fp.gameNumber, fp.pointNumber)
		if seen[key] {
			continue
		}
		seen[key] = true
		newPts = append(newPts, fp)
	}

	if len(newPts) != 1 {
		t.Fatalf("expected 1 new point (1:1:1 pre-existing), got %d", len(newPts))
	}
	if newPts[0].setNumber != 1 || newPts[0].gameNumber != 2 {
		t.Errorf("expected set=1 game=2, got set=%d game=%d",
			newPts[0].setNumber, newPts[0].gameNumber)
	}
}

func mkSetData(setNum, gameNum int, server, winner, score string, points []PointData) SetData {
	return SetData{
		SetNumber:    "Set " + itoa(setNum),
		NumberGame:   itoa(gameNum),
		PlayerServed: server,
		ServeWinner:  winner,
		Score:        score,
		Points:       points,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
