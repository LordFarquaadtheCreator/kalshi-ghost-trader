package algorithms

import (
	"log/slog"
	"testing"
	"time"
)

func cpaTestSetup(t *testing.T) (*ConvexPoolAdaptiveStrategy, *OrderCollector) {
	t.Helper()
	col := NewOrderCollector()
	strat := NewConvexPoolAdaptiveStrategy(col, slog.Default(), DefaultConvexPoolAdaptiveConfig())
	strat.RegisterMarkets("E1", []string{"HOME-MKT", "AWAY-MKT"})
	return strat, col
}

func TestConvexPoolAdaptive_AlphaLowAtMatchStart(t *testing.T) {
	strat, _ := cpaTestSetup(t)

	// Set 1, 0-0 games → progress ~0, alpha should be AlphaMin (0.3).
	alpha := strat.computeAlpha(0, 0, 0, 0)
	if alpha < 0.29 || alpha > 0.31 {
		t.Errorf("expected alpha ~0.3 at match start, got %.4f", alpha)
	}
}

func TestConvexPoolAdaptive_AlphaHighDeepInMatch(t *testing.T) {
	strat, _ := cpaTestSetup(t)

	// Deciding set 3 (sets 1-1), 6-6 games → progress ~1.0, alpha ~AlphaMax (0.8).
	alpha := strat.computeAlpha(1, 1, 6, 6)
	if alpha < 0.79 || alpha > 0.81 {
		t.Errorf("expected alpha ~0.8 deep in match, got %.4f", alpha)
	}
}

func TestConvexPoolAdaptive_AlphaMidGame(t *testing.T) {
	strat, _ := cpaTestSetup(t)

	// Set 1 (0-0 sets), 3-3 games → progress = 0 + 6/12*0.3 = 0.15.
	// alpha = 0.3 + (0.8-0.3)*0.15 = 0.375.
	alpha := strat.computeAlpha(0, 0, 3, 3)
	if alpha < 0.37 || alpha > 0.38 {
		t.Errorf("expected alpha ~0.375 at set 1 mid-game, got %.4f", alpha)
	}
}

func TestConvexPoolAdaptive_AlphaSet2Start(t *testing.T) {
	strat, _ := cpaTestSetup(t)

	// Set 2 start (1-0 sets), 0-0 games → progress = 0.5.
	// alpha = 0.3 + 0.5*0.5 = 0.55.
	alpha := strat.computeAlpha(1, 0, 0, 0)
	if alpha < 0.54 || alpha > 0.56 {
		t.Errorf("expected alpha ~0.55 at set 2 start, got %.4f", alpha)
	}
}

func TestConvexPoolAdaptive_EntryFires(t *testing.T) {
	strat, col := cpaTestSetup(t)
	now := time.Now()

	// Market underpricing home heavily.
	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)

	// Home dominating → high fv, edge will be positive regardless of alpha.
	strat.OnPoint("E1", homeDominatingPoint(now))

	orders := col.Orders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(orders))
	}
	o := orders[0]
	if o.Action != "buy" {
		t.Errorf("expected buy, got %s", o.Action)
	}
	if o.Strategy != "convexpool-adaptive" {
		t.Errorf("expected convexpool-adaptive, got %s", o.Strategy)
	}
	if o.EdgeCents < 3 {
		t.Errorf("expected edge >= 3c, got %d", o.EdgeCents)
	}
}

func TestConvexPoolAdaptive_NoEntryLowEdge(t *testing.T) {
	strat, col := cpaTestSetup(t)
	now := time.Now()

	// Market price near fair value — no edge.
	strat.OnPriceAt("HOME-MKT", 0.95, now)
	strat.OnPriceAt("AWAY-MKT", 0.05, now)

	strat.OnPoint("E1", homeDominatingPoint(now))

	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders (no edge), got %d", len(col.Orders()))
	}
}

func TestConvexPoolAdaptive_AlphaClamped(t *testing.T) {
	strat, _ := cpaTestSetup(t)

	// Impossible state — should clamp to AlphaMax.
	alpha := strat.computeAlpha(5, 5, 20, 20)
	if alpha > 0.81 {
		t.Errorf("expected alpha clamped to <= 0.8, got %.4f", alpha)
	}

	// Negative state — should clamp to AlphaMin.
	alpha = strat.computeAlpha(-1, -1, -1, -1)
	if alpha < 0.29 {
		t.Errorf("expected alpha clamped to >= 0.3, got %.4f", alpha)
	}
}
