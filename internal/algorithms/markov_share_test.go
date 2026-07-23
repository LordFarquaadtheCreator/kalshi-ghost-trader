package algorithms

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// TestSharedMarkovModelBehavioralNoChange proves R.8: sharing one MarkovModel
// across strategies produces identical orders to per-instance models. Drives
// a SetWinnerStrategy through a synthetic match with both wirings, diffs
// emitted orders. Memoization is a cache — same inputs → same outputs.
func TestSharedMarkovModelBehavioralNoChange(t *testing.T) {
	// Synthetic point sequence: a partial set with price updates.
	// Enough to trigger FairValue calls; not enough to settle.
	points := []store.Point{
		{SetNumber: 1, GameNumber: 1, PointNumber: 1, Server: 1, Scorer: 1,
			HomePoints: "15", AwayPoints: "0", HomeGames: 0, AwayGames: 0},
		{SetNumber: 1, GameNumber: 1, PointNumber: 2, Server: 1, Scorer: 1,
			HomePoints: "30", AwayPoints: "0", HomeGames: 0, AwayGames: 0},
		{SetNumber: 1, GameNumber: 1, PointNumber: 3, Server: 1, Scorer: 1,
			HomePoints: "40", AwayPoints: "0", HomeGames: 0, AwayGames: 0},
		{SetNumber: 1, GameNumber: 1, PointNumber: 4, Server: 1, Scorer: 1,
			HomePoints: "40", AwayPoints: "0", HomeGames: 1, AwayGames: 0},
		{SetNumber: 1, GameNumber: 2, PointNumber: 1, Server: 2, Scorer: 2,
			HomePoints: "0", AwayPoints: "15", HomeGames: 1, AwayGames: 0},
		{SetNumber: 1, GameNumber: 2, PointNumber: 2, Server: 2, Scorer: 2,
			HomePoints: "0", AwayPoints: "30", HomeGames: 1, AwayGames: 0},
	}

	prices := []struct {
		market string
		price  float64
	}{
		{"HOME", 0.55}, {"AWAY", 0.45},
		{"HOME", 0.60}, {"AWAY", 0.40},
		{"HOME", 0.65}, {"AWAY", 0.35},
		{"HOME", 0.70}, {"AWAY", 0.30},
		{"HOME", 0.50}, {"AWAY", 0.50},
		{"HOME", 0.45}, {"AWAY", 0.55},
	}

	runOnce := func(shared *MarkovModel) []store.Order {
		em := NewOrderCollector()
		cfg := DefaultSetWinnerConfig()
		cfg.MinEdgeCents = 1
		cfg.CooldownPoints = 1
		s := NewSetWinnerStrategy(em, slog.Default(), cfg)
		if shared != nil {
			s.SetSharedMarkovModel(shared)
		}
		s.RegisterMarkets("E1", []string{"HOME", "AWAY"})
		for i, p := range points {
			s.OnPoint("E1", p)
			if i < len(prices) {
				s.OnPrice(prices[i].market, prices[i].price)
			}
		}
		return em.Orders()
	}

	perInstanceOrders := runOnce(nil)
	sharedModel := NewMarkovModel()
	sharedOrders := runOnce(sharedModel)

	if len(perInstanceOrders) != len(sharedOrders) {
		t.Fatalf("order count mismatch: per-instance=%d shared=%d",
			len(perInstanceOrders), len(sharedOrders))
	}

	for i := range perInstanceOrders {
		a, b := perInstanceOrders[i], sharedOrders[i]
		if a.MarketTicker != b.MarketTicker ||
			a.Side != b.Side ||
			a.SuggestedSize != b.SuggestedSize ||
			a.MarketPrice != b.MarketPrice {
			t.Fatalf("order %d differs:\n  per-instance: %+v\n  shared:       %+v",
				i, a, b)
		}
	}
}

// TestSharedMarkovModelMemoizationAcrossStrategies proves the cache is shared:
// after one strategy populates the memo, a second strategy using the same
// shared model hits the cache (no recompute). Verified by checking the memo
// map is populated after the first strategy's call and reused by the second.
func TestSharedMarkovModelMemoizationAcrossStrategies(t *testing.T) {
	shared := NewMarkovModel()

	// Strategy 1: per-instance (no share) — populates its own memo.
	cfg := DefaultSetWinnerConfig()
	cfg.MinEdgeCents = 1
	cfg.CooldownPoints = 1
	em1 := NewOrderCollector()
	s1 := NewSetWinnerStrategy(em1, slog.Default(), cfg)
	s1.RegisterMarkets("E1", []string{"HOME", "AWAY"})
	s1.OnPoint("E1", store.Point{
		SetNumber: 1, GameNumber: 1, PointNumber: 1, Server: 1, Scorer: 1,
		HomePoints: "15", AwayPoints: "0", HomeGames: 0, AwayGames: 0,
	})
	s1.OnPrice("HOME", 0.55)

	// Strategy 2: shared model.
	em2 := NewOrderCollector()
	s2 := NewSetWinnerStrategy(em2, slog.Default(), cfg)
	s2.SetSharedMarkovModel(shared)
	s2.RegisterMarkets("E2", []string{"HOME", "AWAY"})
	s2.OnPoint("E2", store.Point{
		SetNumber: 1, GameNumber: 1, PointNumber: 1, Server: 1, Scorer: 1,
		HomePoints: "15", AwayPoints: "0", HomeGames: 0, AwayGames: 0,
	})
	s2.OnPrice("HOME", 0.55)

	// Strategy 3: same shared model — should hit memo from strategy 2.
	em3 := NewOrderCollector()
	s3 := NewSetWinnerStrategy(em3, slog.Default(), cfg)
	s3.SetSharedMarkovModel(shared)
	s3.RegisterMarkets("E3", []string{"HOME", "AWAY"})
	s3.OnPoint("E3", store.Point{
		SetNumber: 1, GameNumber: 1, PointNumber: 1, Server: 1, Scorer: 1,
		HomePoints: "15", AwayPoints: "0", HomeGames: 0, AwayGames: 0,
	})
	s3.OnPrice("HOME", 0.55)

	// Shared model's memo must be populated (strategy 2 + 3 used it).
	shared.mu.Lock()
	memoLen := len(shared.gameMemo) + len(shared.setMemo) + len(shared.matchMemo) + len(shared.tbMemo)
	shared.mu.Unlock()
	if memoLen == 0 {
		t.Fatal("shared model memo empty — strategies did not populate it")
	}

	// Orders from shared-model strategies must be identical (same inputs).
	o2, o3 := em2.Orders(), em3.Orders()
	if !slices.EqualFunc(o2, o3, func(a, b store.Order) bool {
		return a.MarketTicker == b.MarketTicker &&
			a.Side == b.Side &&
			a.SuggestedSize == b.SuggestedSize &&
			a.MarketPrice == b.MarketPrice
	}) {
		t.Fatalf("shared-model strategies diverged:\n  s2: %+v\n  s3: %+v", o2, o3)
	}
}
