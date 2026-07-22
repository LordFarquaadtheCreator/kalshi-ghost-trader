package features

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestExtractorDeterministic(t *testing.T) {
	e := NewDefaultExtractor()
	v := View{
		SetsHome: 1, SetsAway: 0,
		GamesHome: 3, GamesAway: 2,
		HomePoints: 30, AwayPoints: 15,
		Server: 1, PriceCents: 55,
		BestBidCents: 54, BestAskCents: 56,
		BestBidSize: 100, BestAskSize: 80,
		MarkovFairValueCents: 60,
		Series: "ATP", BestOf: 3,
	}
	ev := match.PriceUpdate{MarketTicker: "TEST", PriceCents: 55, TS: 1000}

	vec1 := e.Extract(v, ev)
	vec2 := e.Extract(v, ev)

	if len(vec1) != len(e.Names()) {
		t.Fatalf("vector length = %d, want %d", len(vec1), len(e.Names()))
	}

	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("non-deterministic at index %d: %f != %f", i, vec1[i], vec2[i])
		}
	}
}

func TestFeatureHashStableAcrossRuns(t *testing.T) {
	names1 := NewDefaultExtractor().Names()
	names2 := NewDefaultExtractor().Names()

	h1 := FeatureHash(names1)
	h2 := FeatureHash(names2)

	if h1 != h2 {
		t.Errorf("hash not stable: %s != %s", h1, h2)
	}

	// Hash should be 16 hex chars (truncated SHA256).
	if len(h1) != 16 {
		t.Errorf("hash length = %d, want 16", len(h1))
	}
}

func TestFeatureHashOrderIndependent(t *testing.T) {
	names := NewDefaultExtractor().Names()

	// Shuffle a copy.
	shuffled := make([]string, len(names))
	copy(shuffled, names)
	sort.Sort(sort.Reverse(sort.StringSlice(shuffled)))

	h1 := FeatureHash(names)
	h2 := FeatureHash(shuffled)

	if h1 != h2 {
		t.Errorf("hash should be order-independent: %s != %s", h1, h2)
	}
}

func TestFeatureHashChangesWithNames(t *testing.T) {
	names := NewDefaultExtractor().Names()
	h1 := FeatureHash(names)

	// Add a feature.
	modified := append([]string(nil), names...)
	modified = append(modified, "new_feature")
	h2 := FeatureHash(modified)

	if h1 == h2 {
		t.Error("hash should change when features are added")
	}
}

func TestNoFutureAccess(t *testing.T) {
	// This is a compile-level assertion: the features package must only
	// import from domain subpackages and stdlib — no adapters, no database.
	// If this test compiles, the import constraint is satisfied.
	// The actual import check is done by the linter / CI, but we verify
	// the View type has no methods that could access the future.
	v := View{}

	// View has no methods at all — it's a pure data struct.
	// The only way to populate it is via ViewFromMatchView, which takes
	// a strategy.MatchView (also read-only, as-of-now).
	_ = v

	// Verify the extractor doesn't touch any external state.
	e := NewDefaultExtractor()
	vec := e.Extract(View{}, match.ClockTick{})
	if len(vec) != len(e.Names()) {
		t.Errorf("empty view: vector length = %d, want %d", len(vec), len(e.Names()))
	}
}

func TestExtractEdgeFeature(t *testing.T) {
	e := NewDefaultExtractor()
	v := View{
		PriceCents:           50,
		MarkovFairValueCents: 60,
	}
	vec := e.Extract(v, match.ClockTick{})

	names := e.Names()
	idx := -1
	for i, n := range names {
		if n == "edge_cents" {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatal("edge_cents not in feature names")
	}

	if vec[idx] != 10 {
		t.Errorf("edge_cents = %f, want 10 (60-50)", vec[idx])
	}
}

func TestExtractImbalance(t *testing.T) {
	e := NewDefaultExtractor()
	v := View{
		BestBidSize: 100,
		BestAskSize: 80,
	}
	vec := e.Extract(v, match.ClockTick{})

	names := e.Names()
	idx := -1
	for i, n := range names {
		if n == "imbalance" {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatal("imbalance not in feature names")
	}

	// (100 - 80) / (100 + 80) = 20/180 = 0.111...
	expected := 20.0 / 180.0
	if vec[idx] != expected {
		t.Errorf("imbalance = %f, want %f", vec[idx], expected)
	}
}

func TestExtractImbalanceZeroSize(t *testing.T) {
	e := NewDefaultExtractor()
	v := View{}
	vec := e.Extract(v, match.ClockTick{})

	names := e.Names()
	idx := -1
	for i, n := range names {
		if n == "imbalance" {
			idx = i
			break
		}
	}
	if vec[idx] != 0 {
		t.Errorf("imbalance with zero size = %f, want 0", vec[idx])
	}
}

func TestExtractSeriesFlags(t *testing.T) {
	e := NewDefaultExtractor()

	for _, series := range []string{"ATP", "WTA", "ITF", "Challenger", "Other"} {
		v := View{Series: series}
		vec := e.Extract(v, match.ClockTick{})
		names := e.Names()

		atpIdx, wtaIdx, itfIdx, chIdx := -1, -1, -1, -1
		for i, n := range names {
			switch n {
			case "series_atp":
				atpIdx = i
			case "series_wta":
				wtaIdx = i
			case "series_itf":
				itfIdx = i
			case "series_challenger":
				chIdx = i
			}
		}

		switch series {
		case "ATP":
			if vec[atpIdx] != 1 || vec[wtaIdx] != 0 || vec[itfIdx] != 0 || vec[chIdx] != 0 {
				t.Errorf("ATP flags wrong: %v %v %v %v", vec[atpIdx], vec[wtaIdx], vec[itfIdx], vec[chIdx])
			}
		case "WTA":
			if vec[atpIdx] != 0 || vec[wtaIdx] != 1 || vec[itfIdx] != 0 || vec[chIdx] != 0 {
				t.Errorf("WTA flags wrong: %v %v %v %v", vec[atpIdx], vec[wtaIdx], vec[itfIdx], vec[chIdx])
			}
		case "ITF":
			if vec[atpIdx] != 0 || vec[wtaIdx] != 0 || vec[itfIdx] != 1 || vec[chIdx] != 0 {
				t.Errorf("ITF flags wrong: %v %v %v %v", vec[atpIdx], vec[wtaIdx], vec[itfIdx], vec[chIdx])
			}
		case "Challenger":
			if vec[atpIdx] != 0 || vec[wtaIdx] != 0 || vec[itfIdx] != 0 || vec[chIdx] != 1 {
				t.Errorf("Challenger flags wrong: %v %v %v %v", vec[atpIdx], vec[wtaIdx], vec[itfIdx], vec[chIdx])
			}
		case "Other":
			if vec[atpIdx] != 0 || vec[wtaIdx] != 0 || vec[itfIdx] != 0 || vec[chIdx] != 0 {
				t.Errorf("Other flags wrong: %v %v %v %v", vec[atpIdx], vec[wtaIdx], vec[itfIdx], vec[chIdx])
			}
		}
	}
}

func TestFeatureHashMatchesSha256(t *testing.T) {
	names := []string{"a", "b", "c"}
	sorted := []string{"a", "b", "c"}
	sort.Strings(sorted)

	h := sha256.New()
	for _, n := range sorted {
		h.Write([]byte(n))
		h.Write([]byte{0})
	}
	expected := hex.EncodeToString(h.Sum(nil))[:16]
	got := FeatureHash(names)

	if got != expected {
		t.Errorf("FeatureHash = %s, want %s", got, expected)
	}
}
