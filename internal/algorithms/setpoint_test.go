package algorithms

import (
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// TestSetPointMarkovEdge verifies the fatal-flaw fix: setpoint now uses
// Markov match-win probability for edge, not flat set-conversion rate.
// At 5-4, 40-30 serving in set 1 (0-0 sets), the server is at set point.
// Markov match-win prob from this state should be well below 0.93 —
// winning set 1 doesn't guarantee winning the match.
func TestSetPointMarkovEdge(t *testing.T) {
	em := NewOrderCollector()
	cfg := DefaultSetPointConfig()
	cfg.MinEdgeCents = 1
	cfg.CooldownPoints = 0
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	// Seed: establish set tracking at 5-4 in set 1
	seed := store.Point{
		SetNumber: 1, GameNumber: 9, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s.OnPoint("E1", seed)

	// Set price low enough to trigger edge
	s.OnPrice("HOME", 0.50)

	// Trigger: 5-4, 40-30, home serving — home set point
	trig := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "40", AwayPoints: "30",
	}
	s.OnPoint("E1", trig)

	orders := em.Orders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}

	// ConvProb should be Markov match-win prob, NOT 0.93.
	// From 5-4 40-30 serving in set 1 (0-0 sets), match-win prob
	// is around 0.70-0.75 (winning set 1 gives ~70% match-win chance).
	o := orders[0]
	if o.ConvProb >= 0.90 {
		t.Errorf("ConvProb = %.3f, expected < 0.90 (Markov match-win, not flat 0.93)", o.ConvProb)
	}
	if o.ConvProb < 0.50 {
		t.Errorf("ConvProb = %.3f, expected > 0.50 (set point leader should be favored)", o.ConvProb)
	}

	// Edge should be (markov_fv - 0.50) * 100
	expectedEdge := int((o.ConvProb-0.50)*100 + 1e-9)
	if o.EdgeCents != expectedEdge {
		t.Errorf("EdgeCents = %d, want %d", o.EdgeCents, expectedEdge)
	}
}

// TestSetPointTiebreakSetPoint verifies tiebreak set points are now detected.
// Previously, IsTiebreak=true caused detectSetPoint to return nil.
func TestSetPointTiebreakSetPoint(t *testing.T) {
	em := NewOrderCollector()
	cfg := DefaultSetPointConfig()
	cfg.MinEdgeCents = 1
	cfg.CooldownPoints = 0
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	// Seed: 6-6 in set 1 tiebreak, home at 6-5
	seed := store.Point{
		SetNumber: 1, GameNumber: 13, PointNumber: 11,
		HomeGames: 6, AwayGames: 6, Server: 1, Scorer: 1,
		HomePoints: "6", AwayPoints: "5", IsTiebreak: true,
	}
	s.OnPoint("E1", seed)

	s.OnPrice("HOME", 0.55)

	// Trigger: 6-5 in tiebreak, home can win next point → 7-5 → win set
	trig := store.Point{
		SetNumber: 1, GameNumber: 13, PointNumber: 12,
		HomeGames: 6, AwayGames: 6, Server: 2,
		HomePoints: "6", AwayPoints: "5", IsTiebreak: true,
	}
	s.OnPoint("E1", trig)

	orders := em.Orders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (tiebreak set point), got %d", len(orders))
	}
	if orders[0].Context != "home_tb_set_point" {
		t.Errorf("context = %s, want home_tb_set_point", orders[0].Context)
	}
}

// TestSetPointTiebreakNotSetPoint verifies no fire when tiebreak score
// isn't at set point (e.g. 3-2 in tiebreak).
func TestSetPointTiebreakNotSetPoint(t *testing.T) {
	em := NewOrderCollector()
	cfg := DefaultSetPointConfig()
	cfg.MinEdgeCents = 1
	cfg.CooldownPoints = 0
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	seed := store.Point{
		SetNumber: 1, GameNumber: 13, PointNumber: 5,
		HomeGames: 6, AwayGames: 6, Server: 1, Scorer: 1,
		HomePoints: "3", AwayPoints: "2", IsTiebreak: true,
	}
	s.OnPoint("E1", seed)
	s.OnPrice("HOME", 0.55)

	trig := store.Point{
		SetNumber: 1, GameNumber: 13, PointNumber: 6,
		HomeGames: 6, AwayGames: 6, Server: 2,
		HomePoints: "3", AwayPoints: "2", IsTiebreak: true,
	}
	s.OnPoint("E1", trig)

	if len(em.Orders()) != 0 {
		t.Errorf("expected 0 orders (not at tiebreak set point), got %d", len(em.Orders()))
	}
}

// TestSetPointCooldown verifies per-event cooldown prevents over-firing.
func TestSetPointCooldown(t *testing.T) {
	em := NewOrderCollector()
	cfg := DefaultSetPointConfig()
	cfg.MinEdgeCents = 1
	cfg.CooldownPoints = 3
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	// Feed 3 early points to clear cooldown before set point
	for i := 1; i <= 3; i++ {
		s.OnPoint("E1", store.Point{
			SetNumber: 1, GameNumber: i, PointNumber: 1,
			HomeGames: 0, AwayGames: 0, Server: 1, Scorer: 1,
			HomePoints: "0", AwayPoints: "0",
		})
	}
	s.OnPrice("HOME", 0.50)

	// Seed at 5-4 (pointsSinceFire now 4)
	seed := store.Point{
		SetNumber: 1, GameNumber: 9, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s.OnPoint("E1", seed)

	// First fire: 5-4, 40-30 serving (pointsSinceFire=5 >= 3)
	p1 := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "40", AwayPoints: "30",
	}
	s.OnPoint("E1", p1)
	if len(em.Orders()) != 1 {
		t.Fatalf("first fire: expected 1 order, got %d", len(em.Orders()))
	}

	// Feed 2 non-set-point points (pointsSinceFire = 2, below cooldown=3)
	p2 := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 2,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "40", AwayPoints: "40",
	}
	s.OnPoint("E1", p2) // deuce — not set point

	p3 := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 3,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "A", AwayPoints: "40",
	}
	s.OnPoint("E1", p3) // set point again, but only 2 points since fire

	if len(em.Orders()) != 1 {
		t.Errorf("cooldown: expected still 1 order (only 2 points since fire), got %d", len(em.Orders()))
	}

	// Third point — now 3 points since fire, should fire
	p4 := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 4,
		HomeGames: 5, AwayGames: 4, Server: 2,
		HomePoints: "A", AwayPoints: "40",
	}
	s.OnPoint("E1", p4) // set point, 3 points since fire

	if len(em.Orders()) != 2 {
		t.Errorf("after cooldown: expected 2 orders, got %d", len(em.Orders()))
	}
}

// TestSetPointMinEdgeCents verifies default MinEdgeCents=5 filters thin edges.
func TestSetPointMinEdgeCents(t *testing.T) {
	em := NewOrderCollector()
	cfg := DefaultSetPointConfig() // MinEdgeCents=5
	cfg.CooldownPoints = 0
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	seed := store.Point{
		SetNumber: 1, GameNumber: 9, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s.OnPoint("E1", seed)

	// Price at 0.80 — Markov FV from 5-4 40-30 serving set 1 is ~0.72
	// Edge = (0.72 - 0.80) * 100 = -8c → negative, no fire
	s.OnPrice("HOME", 0.80)

	trig := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "40", AwayPoints: "30",
	}
	s.OnPoint("E1", trig)

	if len(em.Orders()) != 0 {
		t.Errorf("expected 0 orders (edge below MinEdgeCents=5), got %d", len(em.Orders()))
	}
}

// TestSetPointSharedMarkovModel verifies shared model produces identical results.
func TestSetPointSharedMarkovModel(t *testing.T) {
	shared := NewMarkovModel()

	runOnce := func(useShared bool) []store.Order {
		em := NewOrderCollector()
		cfg := DefaultSetPointConfig()
		cfg.MinEdgeCents = 1
		cfg.CooldownPoints = 0
		s := NewSetPointStrategy(em, slog.Default(), cfg)
		if useShared {
			s.SetSharedMarkovModel(shared)
		}
		s.RegisterMarkets("E1", []string{"HOME", "AWAY"})
		s.OnPrice("HOME", 0.50)
		s.OnPoint("E1", store.Point{
			SetNumber: 1, GameNumber: 9, PointNumber: 1,
			HomeGames: 5, AwayGames: 4, Server: 1, Scorer: 1,
			HomePoints: "0", AwayPoints: "0",
		})
		s.OnPoint("E1", store.Point{
			SetNumber: 1, GameNumber: 10, PointNumber: 1,
			HomeGames: 5, AwayGames: 4, Server: 1,
			HomePoints: "40", AwayPoints: "30",
		})
		return em.Orders()
	}

	perInstance := runOnce(false)
	sharedOrders := runOnce(true)

	if len(perInstance) != len(sharedOrders) {
		t.Fatalf("order count mismatch: per-instance=%d shared=%d",
			len(perInstance), len(sharedOrders))
	}
	if len(perInstance) == 0 {
		t.Fatal("expected at least 1 order")
	}
	if perInstance[0].ConvProb != sharedOrders[0].ConvProb {
		t.Errorf("ConvProb mismatch: per-instance=%.4f shared=%.4f",
			perInstance[0].ConvProb, sharedOrders[0].ConvProb)
	}
}

// TestSetPointMatchPointAggro verifies matchpoint-aggro config (IncludeSetPoints=false)
// only fires on match points, not regular set points.
func TestSetPointMatchPointAggro(t *testing.T) {
	em := NewOrderCollector()
	cfg := SetPointConfig{
		IncludeSetPoints: false,
		IncludeReturning: true,
		PServe:           0.64,
		MinMarketPrice:   0.05,
		MinEdgeCents:     1,
		CooldownPoints:   0,
		Label:            "matchpoint-aggro",
	}
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	// Set 1, 0-0 sets — set point but NOT match point
	seed := store.Point{
		SetNumber: 1, GameNumber: 9, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s.OnPoint("E1", seed)
	s.OnPrice("HOME", 0.50)

	trig := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "40", AwayPoints: "30",
	}
	s.OnPoint("E1", trig)

	if len(em.Orders()) != 0 {
		t.Errorf("matchpoint-aggro: expected 0 orders on non-match set point, got %d", len(em.Orders()))
	}

	// Now seed at 1-0 sets (home won set 1), set 2 set point = match point
	em2 := NewOrderCollector()
	s2 := NewSetPointStrategy(em2, slog.Default(), cfg)
	s2.RegisterMarkets("E2", []string{"HOME", "AWAY"})

	// Seed set 1 completion: set 2 starts
	seed1 := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 5,
		HomeGames: 6, AwayGames: 4, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s2.OnPoint("E2", seed1)

	seed2 := store.Point{
		SetNumber: 2, GameNumber: 9, PointNumber: 1,
		HomeGames: 5, AwayGames: 3, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s2.OnPoint("E2", seed2)
	s2.OnPrice("HOME", 0.50)

	// Set 2, 5-3, 40-30 serving — match point (home up 1-0, can win set 2 = match)
	trig2 := store.Point{
		SetNumber: 2, GameNumber: 10, PointNumber: 1,
		HomeGames: 5, AwayGames: 3, Server: 1,
		HomePoints: "40", AwayPoints: "30",
	}
	s2.OnPoint("E2", trig2)

	if len(em2.Orders()) != 1 {
		t.Errorf("matchpoint-aggro: expected 1 order on match point, got %d", len(em2.Orders()))
	}
	if em2.Orders()[0].Context != "home_match_point" {
		t.Errorf("context = %s, want home_match_point", em2.Orders()[0].Context)
	}
}

// TestSetPointStalePrice verifies stale price check still works.
func TestSetPointStalePrice(t *testing.T) {
	em := NewOrderCollector()
	cfg := DefaultSetPointConfig()
	cfg.MinEdgeCents = 1
	cfg.CooldownPoints = 0
	s := NewSetPointStrategy(em, slog.Default(), cfg)
	s.RegisterMarkets("E1", []string{"HOME", "AWAY"})

	// Set price with old timestamp via OnPriceAt
	old := time.Now().Add(-2 * time.Minute)
	s.OnPriceAt("HOME", 0.50, old)

	seed := store.Point{
		SetNumber: 1, GameNumber: 9, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1, Scorer: 1,
		HomePoints: "0", AwayPoints: "0",
	}
	s.OnPoint("E1", seed)

	trig := store.Point{
		SetNumber: 1, GameNumber: 10, PointNumber: 1,
		HomeGames: 5, AwayGames: 4, Server: 1,
		HomePoints: "40", AwayPoints: "30",
	}
	s.OnPoint("E1", trig)

	if len(em.Orders()) != 0 {
		t.Errorf("expected 0 orders (stale price), got %d", len(em.Orders()))
	}
}
