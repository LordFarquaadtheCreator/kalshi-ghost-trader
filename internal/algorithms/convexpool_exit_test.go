package algorithms

import (
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func cpeTestSetup(t *testing.T) (*ConvexPoolExitStrategy, *OrderCollector) {
	t.Helper()
	col := NewOrderCollector()
	cfg := DefaultConvexPoolExitConfig()
	cfg.TakeProfitCents = 5
	cfg.StopLossCents = 5
	cfg.MaxHoldSeconds = 300
	cfg.ExitEdgeCents = 0
	strat := NewConvexPoolExitStrategy(col, slog.Default(), cfg)
	strat.RegisterMarkets("E1", []string{"HOME-MKT", "AWAY-MKT"})
	return strat, col
}

// homeDominatingPoint returns a Point where home is up a set, 5-0, 40-0 serving.
// Markov fair value for home will be very high (~0.99).
func homeDominatingPoint(now time.Time) store.Point {
	return store.Point{
		MatchTicker:  "E1",
		TS:           now.UnixMilli(),
		SetNumber:    2,
		GameNumber:   6,
		PointNumber:  1,
		Server:       1,
		HomePoints:   "40",
		AwayPoints:   "0",
		HomeGames:    5,
		AwayGames:    0,
		HomeSetGames: 1, // home won set 1
		AwaySetGames: 0,
	}
}

func TestConvexPoolExit_EntryFires(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	// Set market price low (market underpricing home).
	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)

	// Point where home dominates → fv ~0.99, blended ~0.645, edge ~34c.
	strat.OnPoint("E1", homeDominatingPoint(now))

	orders := col.Orders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 buy order, got %d", len(orders))
	}
	o := orders[0]
	if o.Action != "buy" {
		t.Errorf("expected buy, got %s", o.Action)
	}
	if o.Side != store.OrderSideOpen {
		t.Errorf("expected open side, got %s", o.Side)
	}
	if o.Strategy != "convexpool-exit" {
		t.Errorf("expected convexpool-exit, got %s", o.Strategy)
	}
	if o.EdgeCents < 3 {
		t.Errorf("expected edge >= 3c, got %d", o.EdgeCents)
	}
}

func TestConvexPoolExit_NoEntryLowEdge(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	// Market price already high — close to fair value, no edge.
	strat.OnPriceAt("HOME-MKT", 0.95, now)
	strat.OnPriceAt("AWAY-MKT", 0.05, now)

	strat.OnPoint("E1", homeDominatingPoint(now))

	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders (no edge), got %d", len(col.Orders()))
	}
}

func TestConvexPoolExit_TakeProfit(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)
	strat.OnPoint("E1", homeDominatingPoint(now))

	if len(col.Orders()) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.Orders()))
	}
	entry := col.Orders()[0]
	entryPrice := entry.MarketPrice

	// Price rises by 5c → TP.
	tpPrice := entryPrice + 0.05
	strat.OnPriceAt("HOME-MKT", tpPrice, now.Add(10*time.Second))

	orders := col.Orders()
	if len(orders) != 2 {
		t.Fatalf("expected buy+sell, got %d", len(orders))
	}
	sell := orders[1]
	if sell.Action != "sell" {
		t.Errorf("expected sell, got %s", sell.Action)
	}
	if sell.Side != store.OrderSideClose {
		t.Errorf("expected close side, got %s", sell.Side)
	}
	if sell.EdgeCents <= 0 {
		t.Errorf("expected positive pnl on TP, got %d", sell.EdgeCents)
	}
}

func TestConvexPoolExit_StopLoss(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)
	strat.OnPoint("E1", homeDominatingPoint(now))

	entry := col.Orders()[0]
	entryPrice := entry.MarketPrice

	// Price drops by 5c → SL.
	slPrice := entryPrice - 0.05
	strat.OnPriceAt("HOME-MKT", slPrice, now.Add(10*time.Second))

	orders := col.Orders()
	if len(orders) != 2 {
		t.Fatalf("expected buy+sell, got %d", len(orders))
	}
	sell := orders[1]
	if sell.Action != "sell" {
		t.Errorf("expected sell, got %s", sell.Action)
	}
	if sell.EdgeCents >= 0 {
		t.Errorf("expected negative pnl on SL, got %d", sell.EdgeCents)
	}
}

func TestConvexPoolExit_TimeExit(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)
	strat.OnPoint("E1", homeDominatingPoint(now))

	// Wait beyond MaxHoldSeconds (300s). Price unchanged — no TP/SL.
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(301*time.Second))

	orders := col.Orders()
	if len(orders) != 2 {
		t.Fatalf("expected buy+sell on time exit, got %d", len(orders))
	}
	sell := orders[1]
	if sell.Action != "sell" {
		t.Errorf("expected sell, got %s", sell.Action)
	}
}

func TestConvexPoolExit_NoStacking(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)
	strat.OnPoint("E1", homeDominatingPoint(now))

	if len(col.Orders()) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.Orders()))
	}

	// Another point comes — position already open, should NOT enter again.
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	strat.OnPoint("E1", homeDominatingPoint(now.Add(5*time.Second)))

	if len(col.Orders()) != 1 {
		t.Fatalf("expected no stacking (still 1 order), got %d", len(col.Orders()))
	}
}

func TestConvexPoolExit_ReentryAfterExit(t *testing.T) {
	strat, col := cpeTestSetup(t)
	now := time.Now()

	strat.OnPriceAt("HOME-MKT", 0.30, now)
	strat.OnPriceAt("AWAY-MKT", 0.70, now)
	strat.OnPoint("E1", homeDominatingPoint(now))

	if len(col.Orders()) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.Orders()))
	}

	// TP exit.
	strat.OnPriceAt("HOME-MKT", 0.35, now.Add(10*time.Second))
	if len(col.Orders()) != 2 {
		t.Fatalf("expected buy+sell, got %d", len(col.Orders()))
	}

	// Price drops back, new point arrives — should re-enter.
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(20*time.Second))
	strat.OnPoint("E1", homeDominatingPoint(now.Add(20*time.Second)))

	if len(col.Orders()) != 3 {
		t.Fatalf("expected re-entry (3 orders), got %d", len(col.Orders()))
	}
	third := col.Orders()[2]
	if third.Action != "buy" {
		t.Errorf("expected re-entry buy, got %s", third.Action)
	}
}
