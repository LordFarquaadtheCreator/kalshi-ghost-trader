package sizing

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// TestKellyPropertyNonNegative: result ≥ 0 for all valid inputs.
func TestKellyPropertyNonNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		probBps := rapid.IntRange(0, 10000).Draw(t, "probBps")
		priceCents := rapid.IntRange(1, 99).Draw(t, "priceCents")
		bankrollCents := int64(rapid.IntRange(1, 1_000_000_000).Draw(t, "bankrollCents"))
		fraction := rapid.Float64Range(0.01, 1.0).Draw(t, "fraction")
		legacy := rapid.Bool().Draw(t, "legacy")

		result := KellyContracts(probBps, priceCents, bankrollCents, fraction, legacy)
		if result < 0 {
			t.Fatalf("KellyContracts = %d, want ≥ 0", result)
		}
	})
}

// TestKellyPropertyCorrectModeSpendBound: in correct mode,
// int64(result)*int64(priceCents) ≤ ceil(fraction*edge*bankrollCents).
func TestKellyPropertyCorrectModeSpendBound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		probBps := rapid.IntRange(0, 10000).Draw(t, "probBps")
		priceCents := rapid.IntRange(1, 99).Draw(t, "priceCents")
		bankrollCents := int64(rapid.IntRange(1, 1_000_000_000).Draw(t, "bankrollCents"))
		fraction := rapid.Float64Range(0.01, 1.0).Draw(t, "fraction")

		result := KellyContracts(probBps, priceCents, bankrollCents, fraction, false)
		spend := int64(result) * int64(priceCents)

		prob := float64(probBps) / 10000.0
		price := float64(priceCents) / 100.0
		edge := (prob - price) / (1.0 - price)
		if edge <= 0 {
			return
		}
		upperBound := math.Ceil(fraction * edge * float64(bankrollCents))
		if float64(spend) > upperBound {
			t.Fatalf("correct mode: spend %d > upper bound %.2f", spend, upperBound)
		}
	})
}

// TestKellyPropertyMonotoneInBankroll: result is non-decreasing in bankroll.
func TestKellyPropertyMonotoneInBankroll(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		probBps := rapid.IntRange(0, 10000).Draw(t, "probBps")
		priceCents := rapid.IntRange(1, 99).Draw(t, "priceCents")
		fraction := rapid.Float64Range(0.01, 1.0).Draw(t, "fraction")
		legacy := rapid.Bool().Draw(t, "legacy")

		b1 := int64(rapid.IntRange(1, 1_000_000_000).Draw(t, "b1"))
		b2 := int64(rapid.IntRange(1, 1_000_000_000).Draw(t, "b2"))
		if b1 > b2 {
			b1, b2 = b2, b1
		}

		r1 := KellyContracts(probBps, priceCents, b1, fraction, legacy)
		r2 := KellyContracts(probBps, priceCents, b2, fraction, legacy)
		if r1 > r2 {
			t.Fatalf("non-monotone: bankroll %d → %d, bankroll %d → %d",
				b1, r1, b2, r2)
		}
	})
}

// TestKellyPropertyZeroEdge: zero contracts when edge ≤ 0.
func TestKellyPropertyZeroEdge(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		priceCents := rapid.IntRange(1, 99).Draw(t, "priceCents")
		bankrollCents := int64(rapid.IntRange(1, 1_000_000_000).Draw(t, "bankrollCents"))
		fraction := rapid.Float64Range(0.01, 1.0).Draw(t, "fraction")
		legacy := rapid.Bool().Draw(t, "legacy")

		priceBps := priceCents * 100
		probBps := rapid.IntRange(0, priceBps).Draw(t, "probBps")

		result := KellyContracts(probBps, priceCents, bankrollCents, fraction, legacy)
		if result != 0 {
			t.Fatalf("edge ≤ 0: KellyContracts = %d, want 0", result)
		}
	})
}

// TestKellyLegacyMatchesV1: legacy mode reproduces v1's kellySizeRaw formula.
func TestKellyLegacyMatchesV1(t *testing.T) {
	cases := []struct {
		probBps       int
		priceCents    int
		bankrollCents int64
		fraction      float64
		wantV1        int
	}{
		{6500, 50, 100000, 0.25, 75},
		{7000, 60, 50000, 0.25, 31},
		{5500, 50, 10000, 0.25, 2},
	}
	for _, tc := range cases {
		got := KellyContracts(tc.probBps, tc.priceCents, tc.bankrollCents, tc.fraction, true)
		if got != tc.wantV1 {
			t.Errorf("legacy: probBps=%d priceCents=%d bankroll=%d frac=%.2f → %d, want %d",
				tc.probBps, tc.priceCents, tc.bankrollCents, tc.fraction, got, tc.wantV1)
		}
	}
}

// TestKellyCorrectVsLegacy: correct mode produces ≥ legacy contracts
// because priceCents < 100 always (dividing by smaller number → more contracts).
func TestKellyCorrectVsLegacy(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		probBps := rapid.IntRange(5000, 9900).Draw(t, "probBps")
		priceCents := rapid.IntRange(1, 99).Draw(t, "priceCents")
		bankrollCents := int64(rapid.IntRange(100, 1_000_000_000).Draw(t, "bankrollCents"))
		fraction := rapid.Float64Range(0.01, 1.0).Draw(t, "fraction")

		correct := KellyContracts(probBps, priceCents, bankrollCents, fraction, false)
		legacy := KellyContracts(probBps, priceCents, bankrollCents, fraction, true)

		if correct < legacy {
			t.Fatalf("correct %d < legacy %d (priceCents=%d)",
				correct, legacy, priceCents)
		}
	})
}

// TestSpendCentsExact: SpendCents returns contracts * priceCents exactly.
func TestSpendCentsExact(t *testing.T) {
	cases := []struct {
		contracts  int
		priceCents int
		want       int64
	}{
		{0, 50, 0},
		{1, 50, 50},
		{10, 33, 330},
		{100, 99, 9900},
		{-1, 50, 0},
		{5, 0, 0},
	}
	for _, tc := range cases {
		got := SpendCents(tc.contracts, tc.priceCents)
		if got != tc.want {
			t.Errorf("SpendCents(%d, %d) = %d, want %d", tc.contracts, tc.priceCents, got, tc.want)
		}
	}
}
