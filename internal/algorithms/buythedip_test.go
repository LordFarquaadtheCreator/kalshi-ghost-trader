package algorithms

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func btdTestSetup(t *testing.T) (*BuyTheDipStrategy, *OrderCollector) {
	t.Helper()
	col := NewOrderCollector()
	strat := NewBuyTheDipStrategy(col, slog.Default(), DefaultBuyTheDipConfig())
	strat.RegisterMarkets("E1", []string{"HOME-MKT", "AWAY-MKT"})
	return strat, col
}

func TestBuyTheDip_NoDipNoOrder(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Price stable at 0.60 — no dip.
	strat.OnPriceAt("HOME-MKT", 0.60, now)
	strat.OnPriceAt("HOME-MKT", 0.59, now.Add(10*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.58, now.Add(20*time.Second))
	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_DipTriggersBuy(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Pre-dip at 0.65 (favourite), then drop to 0.30 (35c dip).
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
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
	if o.MarketTicker != "HOME-MKT" {
		t.Errorf("expected HOME-MKT, got %s", o.MarketTicker)
	}
	if o.Strategy != "buythedip" {
		t.Errorf("expected buythedip, got %s", o.Strategy)
	}
}

func TestBuyTheDip_SmallDipNoTrigger(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// 15c dip — below 30c threshold.
	strat.OnPriceAt("HOME-MKT", 0.60, now)
	strat.OnPriceAt("HOME-MKT", 0.45, now.Add(5*time.Second))
	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders for small dip, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_NotFavouriteNoTrigger(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Pre-dip at 0.40 (not favourite), drop to 0.05 (35c dip).
	// MinPreDipPrice=0.50 — should not fire.
	strat.OnPriceAt("HOME-MKT", 0.40, now)
	strat.OnPriceAt("HOME-MKT", 0.05, now.Add(5*time.Second))
	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders for non-favourite dip, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_EntryPriceOutOfRange(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Pre-dip at 0.95, drop to 0.85 (10c dip — below threshold anyway).
	// Use a bigger dip: 0.95 -> 0.82 (13c, still below 30c).
	// Better test: 0.95 -> 0.50 (45c dip, entry 0.50 is in range).
	// For out-of-range: 0.95 -> 0.90 (5c, below threshold).
	// Actually test MaxEntryPrice: pre-dip 0.95, drop to 0.82 (13c, below 30c).
	// Need 30c+ dip that lands above 0.80: pre-dip 0.99, drop to 0.85 (14c, below).
	// Can't get 30c dip above 0.80 with pre-dip <1.0. Skip — logic prevents it.
	// Test MinEntryPrice: pre-dip 0.55, drop to 0.10 (45c dip, entry 0.10 < 0.15).
	strat.OnPriceAt("HOME-MKT", 0.55, now)
	strat.OnPriceAt("HOME-MKT", 0.10, now.Add(5*time.Second))
	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders for entry below min, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_MatchPointExcluded(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Set match point context.
	strat.OnPoint("E1", store.Point{
		MatchTicker:   "E1",
		TS:            now.UnixMilli(),
		IsMatchPoint:  true,
	})
	// Dip should be excluded.
	strat.OnPriceAt("HOME-MKT", 0.65, now.Add(1*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(6*time.Second))
	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders at match point, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_SetPointExcluded(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	strat.OnPoint("E1", store.Point{
		MatchTicker:  "E1",
		TS:           now.UnixMilli(),
		IsSetPoint:   true,
	})
	strat.OnPriceAt("HOME-MKT", 0.65, now.Add(1*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(6*time.Second))
	if len(col.Orders()) != 0 {
		t.Fatalf("expected 0 orders at set point, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_TakeProfitExit(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Dip: 0.65 -> 0.30 (35c dip). TP = 0.30 + 0.75*0.35 = 0.5625.
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	if len(col.Orders()) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.Orders()))
	}
	// Price recovers to 0.57 — above TP 0.5625.
	strat.OnPriceAt("HOME-MKT", 0.57, now.Add(10*time.Second))
	orders := col.Orders()
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders (buy+sell), got %d", len(orders))
	}
	sell := orders[1]
	if sell.Action != "sell" {
		t.Errorf("expected sell, got %s", sell.Action)
	}
	if sell.Side != store.OrderSideClose {
		t.Errorf("expected close side, got %s", sell.Side)
	}
	// TP price = 0.5625, entry = 0.30, pnl = +26.25c
	if sell.EdgeCents <= 0 {
		t.Errorf("expected positive edge on TP, got %d", sell.EdgeCents)
	}
}

func TestBuyTheDip_StopLossExit(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Dip: 0.65 -> 0.30 (35c dip). SL = 0.30 - 0.10 = 0.20.
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	// Price drops to 0.19 — below SL 0.20.
	strat.OnPriceAt("HOME-MKT", 0.19, now.Add(10*time.Second))
	orders := col.Orders()
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders (buy+sell), got %d", len(orders))
	}
	sell := orders[1]
	if sell.Action != "sell" {
		t.Errorf("expected sell, got %s", sell.Action)
	}
	// SL price = 0.20, entry = 0.30, pnl = -10c
	if sell.EdgeCents >= 0 {
		t.Errorf("expected negative edge on SL, got %d", sell.EdgeCents)
	}
}

func TestBuyTheDip_TimeExit(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// Dip: 0.65 -> 0.30 (35c dip).
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	// Price stays flat — no TP, no SL. Time exit at 300s.
	strat.OnPriceAt("HOME-MKT", 0.35, now.Add(310*time.Second))
	orders := col.Orders()
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders (buy+sell), got %d", len(orders))
	}
	sell := orders[1]
	if sell.Action != "sell" {
		t.Errorf("expected sell, got %s", sell.Action)
	}
	// Time exit at 0.35, entry 0.30, pnl = +5c
	if sell.EdgeCents != 5 {
		t.Errorf("expected +5c edge on time exit, got %d", sell.EdgeCents)
	}
}

func TestBuyTheDip_NoStacking(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// First dip triggers buy.
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	if len(col.Orders()) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.Orders()))
	}
	// Price moves but no TP/SL hit — should NOT trigger a second buy.
	strat.OnPriceAt("HOME-MKT", 0.40, now.Add(10*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.45, now.Add(15*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.50, now.Add(20*time.Second))
	if len(col.Orders()) != 1 {
		t.Fatalf("expected no stacking (still 1 order), got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_ExitThenReentry(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	// First dip + TP exit.
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.57, now.Add(10*time.Second))
	if len(col.Orders()) != 2 {
		t.Fatalf("expected 2 orders after TP, got %d", len(col.Orders()))
	}
	// New dip after exit — should be able to enter again.
	strat.OnPriceAt("HOME-MKT", 0.70, now.Add(20*time.Second))
	strat.OnPriceAt("HOME-MKT", 0.35, now.Add(25*time.Second))
	orders := col.Orders()
	if len(orders) != 3 {
		t.Fatalf("expected 3 orders after re-entry, got %d", len(orders))
	}
	if orders[2].Action != "buy" {
		t.Errorf("expected buy on re-entry, got %s", orders[2].Action)
	}
}

func TestBuyTheDip_UnregisterClearsState(t *testing.T) {
	strat, col := btdTestSetup(t)
	now := time.Now()
	strat.OnPriceAt("HOME-MKT", 0.65, now)
	strat.OnPriceAt("HOME-MKT", 0.30, now.Add(5*time.Second))
	strat.UnregisterMarkets("E1")
	// After unregister, no more orders.
	strat.OnPriceAt("HOME-MKT", 0.57, now.Add(10*time.Second))
	if len(col.Orders()) != 1 {
		t.Fatalf("expected 1 order after unregister, got %d", len(col.Orders()))
	}
}

func TestBuyTheDip_PreMatchGated(t *testing.T) {
	strat, _ := btdTestSetup(t)
	// PreMatchGated is a marker interface — just verify it implements it.
	var _ PreMatchGated = strat
}

func TestBuyTheDip_ScoreObserver(t *testing.T) {
	strat, _ := btdTestSetup(t)
	// ScoreObserver is an optional interface — verify it implements it.
	var _ ScoreObserver = strat
}

func TestBuyTheDip_OnTickNoOp(t *testing.T) {
	strat, _ := btdTestSetup(t)
	// OnTick should not panic.
	strat.OnTick(context.Background())
}
