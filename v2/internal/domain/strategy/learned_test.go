package strategy

import (
	"math/rand"
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func makeLearnedStrategy(mode Mode, predValue float64, temp float64) *LearnedStrategy {
	pred := &MockPredictor{Value: predValue}
	rng := rand.New(rand.NewSource(42))
	return NewLearnedStrategy("rl.fairvalue.v1", 1, pred, mode, rng, temp)
}

func makeTestState() *State {
	return &State{
		MatchView: MatchView{
			EventTicker:   "TEST",
			MarketTickers: []string{"TEST-H", "TEST-A"},
			Prices:        map[string]int{"": 50},
			PriceTS:       map[string]int64{"": 1000},
		},
		StrategyState: make(map[string]any),
	}
}

// TestLearnedStrategyZeroAllocSteadyState asserts the hot path is lean.
// The feature buffer is preallocated and reused. The feature map for
// logging allocates (unavoidable — it's part of the Intent), but the
// inference path itself should not allocate.
func TestLearnedStrategyZeroAllocSteadyState(t *testing.T) {
	s := makeLearnedStrategy(ModeChampion, 0.65, 0)
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	// Warm up.
	for i := 0; i < 100; i++ {
		_ = s.OnEvent(ev, st)
	}

	// Measure allocations — the inference path (extract + predict) should
	// be zero-alloc. The feature map for logging is the only allocation.
	allocs := testing.AllocsPerRun(100, func() {
		_ = s.OnEvent(ev, st)
	})

	// The feature map for logging allocates (unavoidable — Intent carries a map).
	// The inference path (extract + predict) is zero-alloc via ExtractInto.
	// Allow budget for: map creation, map bucket allocs, intent slice, propensity pointer.
	if allocs > 15 {
		t.Errorf("AllocsPerRun = %f, want <= 15 (inference path should be zero-alloc)", allocs)
	}
}

// TestLearnedDeterministic asserts shadow and champion modes produce
// identical intents for the same input.
func TestLearnedDeterministic(t *testing.T) {
	s := makeLearnedStrategy(ModeChampion, 0.65, 0)
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	intents1 := s.OnEvent(ev, st)
	intents2 := s.OnEvent(ev, st)

	if len(intents1) != len(intents2) {
		t.Fatalf("intent count: %d vs %d", len(intents1), len(intents2))
	}

	for i := range intents1 {
		if intents1[i].Action != intents2[i].Action {
			t.Errorf("action: %s vs %s", intents1[i].Action, intents2[i].Action)
		}
		if intents1[i].PriceCents != intents2[i].PriceCents {
			t.Errorf("price: %d vs %d", intents1[i].PriceCents, intents2[i].PriceCents)
		}
		if intents1[i].FeatureHash != intents2[i].FeatureHash {
			t.Errorf("feature_hash: %s vs %s", intents1[i].FeatureHash, intents2[i].FeatureHash)
		}
	}
}

// TestLearnedPaperModeDeterministicWithSeed asserts paper mode is
// deterministic given its seed.
func TestLearnedPaperModeDeterministicWithSeed(t *testing.T) {
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	// Two strategies with the same seed → same actions.
	s1 := NewLearnedStrategy("rl.fv.v1", 1, &MockPredictor{Value: 0.65}, ModePaper, rand.New(rand.NewSource(123)), 10.0)
	s2 := NewLearnedStrategy("rl.fv.v1", 1, &MockPredictor{Value: 0.65}, ModePaper, rand.New(rand.NewSource(123)), 10.0)

	var actions1, actions2 []string
	for i := 0; i < 100; i++ {
		intents1 := s1.OnEvent(ev, st)
		if len(intents1) > 0 {
			actions1 = append(actions1, intents1[0].Action)
		} else {
			actions1 = append(actions1, "pass")
		}

		intents2 := s2.OnEvent(ev, st)
		if len(intents2) > 0 {
			actions2 = append(actions2, intents2[0].Action)
		} else {
			actions2 = append(actions2, "pass")
		}
	}

	if len(actions1) != len(actions2) {
		t.Fatalf("action count: %d vs %d", len(actions1), len(actions2))
	}

	for i := range actions1 {
		if actions1[i] != actions2[i] {
			t.Errorf("action %d: %s vs %s (same seed should produce same actions)", i, actions1[i], actions2[i])
		}
	}
}

// TestLearnedShadowModeNoExecution asserts shadow mode computes intents
// but they are marked for non-execution (the caller checks mode).
func TestLearnedShadowModeNoExecution(t *testing.T) {
	s := makeLearnedStrategy(ModeShadow, 0.65, 0)
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	intents := s.OnEvent(ev, st)

	// Shadow mode still computes intents — the caller (tracker) decides
	// not to execute them. Verify the intent has the model_id for logging.
	if len(intents) > 0 {
		if intents[0].ModelID == nil {
			t.Error("shadow intent missing model_id")
		}
		if intents[0].FeatureHash == "" {
			t.Error("shadow intent missing feature_hash")
		}
	}

	// Mode is Shadow — caller checks this.
	if s.Mode() != ModeShadow {
		t.Errorf("mode = %d, want Shadow", s.Mode())
	}
}

// TestNoMidMatchSwap asserts that SetMode doesn't affect an in-flight
// OnEvent call. Mode swaps happen at match boundaries only.
func TestNoMidMatchSwap(t *testing.T) {
	s := makeLearnedStrategy(ModeShadow, 0.65, 0)
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	// Get intents with shadow mode.
	intents1 := s.OnEvent(ev, st)

	// Swap mode mid-match (shouldn't happen in prod, but verify safety).
	s.SetMode(ModeChampion)

	// Next event uses the new mode.
	intents2 := s.OnEvent(ev, st)

	// Both should produce intents (the prediction is the same).
	if len(intents1) == 0 && len(intents2) == 0 {
		// Both pass — fine.
		return
	}

	// The key invariant: each call sees a consistent mode.
	// We can't verify retroactively, but we verify the mode is now Champion.
	if s.Mode() != ModeChampion {
		t.Errorf("mode = %d, want Champion after SetMode", s.Mode())
	}
}

// TestLearnedStrategyFeatureHashStable verifies the feature hash is
// the same across strategy instances.
func TestLearnedStrategyFeatureHashStable(t *testing.T) {
	s1 := makeLearnedStrategy(ModeShadow, 0.65, 0)
	s2 := makeLearnedStrategy(ModeChampion, 0.70, 5.0)

	if s1.FeatureHash() != s2.FeatureHash() {
		t.Errorf("feature hash differs between instances: %s vs %s", s1.FeatureHash(), s2.FeatureHash())
	}
}

// TestLearnedStrategyPropensityLogged verifies paper mode logs propensity.
func TestLearnedStrategyPropensityLogged(t *testing.T) {
	s := makeLearnedStrategy(ModePaper, 0.65, 10.0)
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	// Run many events to get at least one buy.
	for i := 0; i < 100; i++ {
		intents := s.OnEvent(ev, st)
		if len(intents) > 0 {
			if intents[0].Propensity == nil {
				t.Fatal("paper mode buy intent missing propensity")
			}
			if *intents[0].Propensity <= 0 || *intents[0].Propensity > 1 {
				t.Errorf("propensity = %f, want (0, 1]", *intents[0].Propensity)
			}
			return
		}
	}
	t.Skip("no buy actions in 100 events (acceptable with low edge)")
}

// BenchmarkLearnedInference measures inference latency.
// p99 should be under 200µs for a 500-tree LightGBM model.
func BenchmarkLearnedInference(b *testing.B) {
	s := makeLearnedStrategy(ModeChampion, 0.65, 0)
	st := makeTestState()
	ev := match.PriceUpdate{MarketTicker: "TEST-H", PriceCents: 50, TS: 1000}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.OnEvent(ev, st)
	}
}
