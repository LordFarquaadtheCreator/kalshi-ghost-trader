package match

import "testing"

// TestMergerDedup: first occurrence forwards, duplicate is dropped.
func TestMergerDedup(t *testing.T) {
	m := NewMerger()

	ev1 := PointScored{
		EventTicker: "E1",
		Point:       Point{HomePoints: "15", AwayPoints: "0", HomeGames: 1, AwayGames: 0},
		TS:         1000,
	}
	ev2 := PointScored{
		EventTicker: "E1",
		Point:       Point{HomePoints: "15", AwayPoints: "0", HomeGames: 1, AwayGames: 0},
		TS:         2000, // same score state, different TS
	}
	ev3 := PointScored{
		EventTicker: "E1",
		Point:       Point{HomePoints: "30", AwayPoints: "0", HomeGames: 1, AwayGames: 0},
		TS:         3000, // different score state
	}

	// First occurrence of 15-0 → forwarded.
	if fwd := m.Forward(ev1); fwd == nil {
		t.Fatal("first occurrence dropped, want forwarded")
	}

	// Duplicate 15-0 → dropped.
	if fwd := m.Forward(ev2); fwd != nil {
		t.Fatal("duplicate forwarded, want dropped")
	}

	// New score 30-0 → forwarded.
	if fwd := m.Forward(ev3); fwd == nil {
		t.Fatal("new score dropped, want forwarded")
	}

	if m.DupCount.Load() != 1 {
		t.Errorf("DupCount = %d, want 1", m.DupCount.Load())
	}
}

// TestMergerInterleaved: primary and backup sources interleaved, only first
// occurrence of each score state forwards.
func TestMergerInterleaved(t *testing.T) {
	m := NewMerger()

	states := []PointScored{
		// Primary source
		{EventTicker: "E", Point: Point{HomePoints: "15", AwayPoints: "0", HomeGames: 0, AwayGames: 0}, TS: 100},
		{EventTicker: "E", Point: Point{HomePoints: "30", AwayPoints: "0", HomeGames: 0, AwayGames: 0}, TS: 200},
		// Backup source catches up — duplicates
		{EventTicker: "E", Point: Point{HomePoints: "15", AwayPoints: "0", HomeGames: 0, AwayGames: 0}, TS: 150},
		{EventTicker: "E", Point: Point{HomePoints: "30", AwayPoints: "0", HomeGames: 0, AwayGames: 0}, TS: 250},
		// New state from primary
		{EventTicker: "E", Point: Point{HomePoints: "40", AwayPoints: "0", HomeGames: 0, AwayGames: 0}, TS: 300},
		// Backup duplicate
		{EventTicker: "E", Point: Point{HomePoints: "40", AwayPoints: "0", HomeGames: 0, AwayGames: 0}, TS: 350},
	}

	forwarded := 0
	for _, ev := range states {
		if m.Forward(ev) != nil {
			forwarded++
		}
	}

	if forwarded != 3 {
		t.Errorf("forwarded %d, want 3 (15-0, 30-0, 40-0)", forwarded)
	}
	if m.DupCount.Load() != 3 {
		t.Errorf("DupCount = %d, want 3", m.DupCount.Load())
	}
}
