package algorithms

import (
	"math"
	"testing"
)

const eps = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < eps
}

func TestTbPointValue(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"1", 1},
		{"2", 2},
		{"3", 3},
		{"4", 4},
		{"5", 5},
		{"6", 6},
		{"7", 7},
		{"12", 12},
		{"", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		got := tbPointValue(c.in)
		if got != c.want {
			t.Errorf("tbPointValue(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPointValueMarkovUnchanged(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"15", 1},
		{"30", 2},
		{"40", 3},
		{"A", 4},
		{"", 0},
		{"1", 0}, // regular game points don't use numeric strings
	}
	for _, c := range cases {
		got := pointValueMarkov(c.in)
		if got != c.want {
			t.Errorf("pointValueMarkov(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTiebreakFromZeroZeroSymmetric(t *testing.T) {
	m := NewMarkovModel()
	// With pReturn = 1 - pServe, the model is perfectly symmetric.
	// Tiebreak from 0-0 is exactly 0.5 regardless of who serves first.
	pHome := m.tiebreakWinProb("0", "0", true)
	pAway := m.tiebreakWinProb("0", "0", false)
	if !approxEqual(pHome, 0.5) {
		t.Errorf("tiebreak 0-0 home serving = %.6f, want 0.5 (symmetric model)", pHome)
	}
	if !approxEqual(pAway, 0.5) {
		t.Errorf("tiebreak 0-0 away serving = %.6f, want 0.5 (symmetric model)", pAway)
	}
}

func TestTiebreakSymmetry(t *testing.T) {
	m := NewMarkovModel()
	// P(home wins TB | home serves first) + P(home wins TB | away serves first) = 1
	// by symmetry (swap home/away, pServe ↔ pReturn).
	pHome := m.tiebreakWinProb("0", "0", true)
	pAway := m.tiebreakWinProb("0", "0", false)
	if !approxEqual(pHome+pAway, 1.0) {
		t.Errorf("symmetry: %.4f + %.4f = %.4f, want 1.0", pHome, pAway, pHome+pAway)
	}
}

func TestTiebreakMidScore(t *testing.T) {
	m := NewMarkovModel()
	// At 6-0 in tiebreak, home needs 1 point to win. Very high probability.
	// Home serving next point at 6-0: total points = 6, point 6 served by...
	// Pattern: H,A,A,H,H,A → point 6 is away serving.
	// But home has 6-0 lead, so P(home wins) is very high regardless.
	p := m.tiebreakWinProb("6", "0", false)
	if p < 0.95 {
		t.Errorf("tiebreak 6-0 = %.4f, expected > 0.95", p)
	}

	// At 0-6, home should have very low probability.
	p2 := m.tiebreakWinProb("0", "6", true)
	if p2 > 0.05 {
		t.Errorf("tiebreak 0-6 = %.4f, expected < 0.05", p2)
	}
}

func TestTiebreakMatchPoint(t *testing.T) {
	m := NewMarkovModel()
	// At 6-5 in tiebreak, home needs 1 point to win 7-5.
	// totalPoints = 11. Serve pattern: 0:H,1:A,2:A,3:H,4:H,5:A,6:A,7:H,8:H,9:A,10:A,11:H
	// Point 11 served by home. p = pServe = 0.64.
	// But if home loses, 6-6 → deuce territory. So P > 0.64.
	p := m.tiebreakWinProb("6", "5", true)
	if p <= 0.64 {
		t.Errorf("tiebreak 6-5 home serving = %.4f, expected > 0.64 (win point OR deuce)", p)
	}
}

func TestSetWinProbAtSixSix(t *testing.T) {
	m := NewMarkovModel()
	// At 6-6, setWinProb uses actual tiebreak model.
	// With symmetric pServe/pReturn, 0-0 tiebreak = 0.5 regardless of server.
	pHomeServes := m.setWinProb(6, 6, 0, 1, false)
	pAwayServes := m.setWinProb(6, 6, 0, 2, false)

	// Symmetric model → both 0.5
	if !approxEqual(pHomeServes, 0.5) {
		t.Errorf("setWinProb(6,6,server=1) = %.6f, want 0.5 (symmetric)", pHomeServes)
	}
	if !approxEqual(pAwayServes, 0.5) {
		t.Errorf("setWinProb(6,6,server=2) = %.6f, want 0.5 (symmetric)", pAwayServes)
	}

	// With asymmetric pServe, mid-tiebreak scores should differ from 0.5.
	// At 5-3 home, home needs 2 points to win. Serve pattern at totalPoints=8:
	// 0:H,1:A,2:A,3:H,4:H,5:A,6:A,7:H,8:H → point 8 served by home.
	m2 := NewMarkovModelWithProb(0.70)
	p2 := m2.setWinProb(6, 6, 0, 1, true)
	_ = p2 // setWinProb with isTiebreak=true returns pHomeGame directly
	// Test via tiebreakWinProb instead
	pTB := m2.tiebreakWinProb("5", "3", true)
	if approxEqual(pTB, 0.5) {
		t.Error("tiebreak 5-3 with pServe=0.70 returns 0.5 — model not working")
	}
	if pTB < 0.8 {
		t.Errorf("tiebreak 5-3 home serving with pServe=0.70 = %.4f, expected > 0.8", pTB)
	}
}

func TestSetWinProbSixSixMatchesTiebreak(t *testing.T) {
	m := NewMarkovModel()
	// setWinProb at 6-6 with server=1 should equal tiebreakWinProb("0","0",true)
	pSet := m.setWinProb(6, 6, 0, 1, false)
	pTB := m.tiebreakWinProb("0", "0", true)
	if !approxEqual(pSet, pTB) {
		t.Errorf("setWinProb(6,6,server=1) = %.6f, tiebreakWinProb(0,0,home) = %.6f, should match",
			pSet, pTB)
	}

	pSet2 := m.setWinProb(6, 6, 0, 2, false)
	pTB2 := m.tiebreakWinProb("0", "0", false)
	if !approxEqual(pSet2, pTB2) {
		t.Errorf("setWinProb(6,6,server=2) = %.6f, tiebreakWinProb(0,0,away) = %.6f, should match",
			pSet2, pTB2)
	}
}

func TestWinProbabilityTiebreakMidScore(t *testing.T) {
	m := NewMarkovModel()
	// WinProbability with isTiebreak=true and mid-tiebreak score.
	// Before the fix, pointValueMarkov("3") = 0, so this was computed as 0-0.
	// Now tbPointValue("3") = 3, so it correctly uses 3-2 score.
	p := m.WinProbability(1, 0, 6, 6, "3", "2", 1, true)
	// Home is up 1 set, up 3-2 in tiebreak. Should be high probability.
	if p < 0.7 {
		t.Errorf("WinProbability(1-0, 6-6, 3-2, home serving, TB) = %.4f, expected > 0.7", p)
	}
}

func TestWinProbabilityTiebreakNotFiftyFifty(t *testing.T) {
	// With asymmetric pServe, mid-tiebreak scores should not be 50/50.
	// At 5-3, home is close to winning. With pServe=0.70 and home serving, P > 0.8.
	m := NewMarkovModelWithProb(0.70)
	p := m.WinProbability(1, 1, 6, 6, "5", "3", 1, true)
	if approxEqual(p, 0.5) {
		t.Error("WinProbability at 5-3 tiebreak with pServe=0.70 returns 0.5")
	}
	if p < 0.8 {
		t.Errorf("WinProbability(1-1, 6-6, 5-3, home serving, TB) = %.4f, expected > 0.8", p)
	}
}

func TestFairValueClamping(t *testing.T) {
	m := NewMarkovModel()
	// FairValue should clamp to [0.01, 0.99]
	p := m.FairValue(2, 0, 0, 0, "0", "0", 1, false)
	if p != 0.99 {
		t.Errorf("FairValue(match won) = %.4f, want 0.99 (clamped)", p)
	}
	p2 := m.FairValue(0, 2, 0, 0, "0", "0", 1, false)
	if p2 != 0.01 {
		t.Errorf("FairValue(match lost) = %.4f, want 0.01 (clamped)", p2)
	}
}

func TestCustomServeProb(t *testing.T) {
	// With pServe = 0.5, tiebreak should be exactly 50/50 regardless of server
	m := NewMarkovModelWithProb(0.5)
	p := m.tiebreakWinProb("0", "0", true)
	if !approxEqual(p, 0.5) {
		t.Errorf("tiebreak with pServe=0.5, home serving = %.4f, want 0.5", p)
	}
	p2 := m.tiebreakWinProb("0", "0", false)
	if !approxEqual(p2, 0.5) {
		t.Errorf("tiebreak with pServe=0.5, away serving = %.4f, want 0.5", p2)
	}
}
