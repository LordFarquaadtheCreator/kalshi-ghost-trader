package pricing

import (
	"math"
	"testing"
)

// TestTiebreakDeuceClosedForm asserts the 6-6 tiebreak deuce value equals
// a direct two-point recursion truncated at depth 40, within 1e-9, for
// pServe ∈ {0.55, 0.65, 0.75}.
//
// The corrected formula uses pServe*pReturn (serve alternates in tiebreak)
// instead of v1's pAvg² (which assumed same server for both points).
// The threshold is also corrected: 6-6 is deuce in a first-to-7 tiebreak,
// not 7-7.
func TestTiebreakDeuceClosedForm(t *testing.T) {
	for _, pServe := range []float64{0.55, 0.65, 0.75} {
		m := NewMarkovModelWithProb(pServe)
		got := m.tiebreakWinProb("6", "6", true)
		want := tbDeuceBruteForce(pServe, 40)

		if math.Abs(got-want) > 1e-9 {
			t.Errorf("pServe=%.2f: tiebreak deuce closed form = %.12f, brute force = %.12f, diff = %.2e",
				pServe, got, want, math.Abs(got-want))
		}
	}
}

// tbDeuceBruteForce computes P(home wins tiebreak from 6-6) by direct
// recursion with alternating serve, truncated at maxDepth.
func tbDeuceBruteForce(pServe float64, maxDepth int) float64 {
	pReturn := 1 - pServe
	var rec func(h, a int, homeServing bool, depth int) float64
	rec = func(h, a int, homeServing bool, depth int) float64 {
		if h >= 7 && h-a >= 2 {
			return 1.0
		}
		if a >= 7 && a-h >= 2 {
			return 0.0
		}
		if depth > maxDepth {
			return 0.5
		}
		p := pServe
		if !homeServing {
			p = pReturn
		}
		totalPoints := h + a
		var nextHomeServing bool
		if totalPoints%2 == 0 {
			nextHomeServing = !homeServing
		} else {
			nextHomeServing = homeServing
		}
		return p*rec(h+1, a, nextHomeServing, depth+1) +
			(1-p)*rec(h, a+1, nextHomeServing, depth+1)
	}
	return rec(6, 6, true, 0)
}

// TestGoldenValues checks 10 mid-match states against committed golden values.
// Computed once with the corrected model. Any change that shifts these
// values is a behavior change that must be deliberate.
func TestGoldenValues(t *testing.T) {
	tests := []struct {
		name        string
		setsH       int
		setsA       int
		gamesH      int
		gamesA      int
		homePts     string
		awayPts     string
		server      int
		isTB        bool
		wantWinProb float64
		tol         float64
	}{
		{"pre_match_home_serving", 0, 0, 0, 0, "0", "0", 1, false, 0.5220, 0.001},
		{"home_up_set_1", 1, 0, 0, 0, "0", "0", 1, false, 0.7720, 0.001},
		{"away_up_set_1", 0, 1, 0, 0, "0", "0", 1, false, 0.2720, 0.001},
		{"deciding_set_start", 1, 1, 0, 0, "0", "0", 1, false, 0.5440, 0.001},
		{"set1_3_2_40_30_home_serving", 0, 0, 3, 2, "40", "30", 1, false, 0.6474, 0.001},
		{"set1_5_4_30_40_away_serving", 0, 0, 5, 4, "30", "40", 2, false, 0.5216, 0.001},
		{"tiebreak_0_0_home_serving", 0, 0, 6, 6, "0", "0", 1, true, 0.5000, 0.001},
		{"deciding_5_3_40_0", 1, 1, 5, 3, "40", "0", 1, false, 0.9964, 0.001},
		{"deciding_tb_4_2", 1, 1, 6, 6, "4", "2", 1, true, 0.7831, 0.001},
		{"home_up_set_break_point", 1, 0, 5, 3, "40", "30", 2, false, 0.9562, 0.001},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMarkovModelWithProb(0.64)
			got := m.WinProbability(tc.setsH, tc.setsA, tc.gamesH, tc.gamesA,
				tc.homePts, tc.awayPts, tc.server, tc.isTB)
			if math.Abs(got-tc.wantWinProb) > tc.tol {
				t.Errorf("WinProb = %.4f, want %.4f (±%.4f)", got, tc.wantWinProb, tc.tol)
			}
		})
	}
}

// TestFairValueClamping verifies the [0.01, 0.99] clamp.
func TestFairValueClamping(t *testing.T) {
	m := NewMarkovModelWithProb(0.64)

	fv := m.FairValue(1, 0, 5, 0, "40", "0", 1, false)
	if fv > 0.99 {
		t.Errorf("FairValue = %.4f, should be clamped to <= 0.99", fv)
	}

	fv = m.FairValue(0, 1, 0, 5, "0", "40", 2, false)
	if fv < 0.01 {
		t.Errorf("FairValue = %.4f, should be clamped to >= 0.01", fv)
	}
}
