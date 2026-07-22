package positions

import (
	"context"
	"fmt"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setup opens an in-memory sqlite DB with the Position + Order tables.
// Uses sqlite for test speed — production is postgres, but gorm
// abstracts the schema. Each test gets a fresh DB via unique DSN to
// avoid cross-test contamination (sqlite shared cache would otherwise
// persist rows across tests).
func setup(t *testing.T) (*Manager, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&store.Position{}, &store.Order{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Override now to deterministic value for tests.
	nowMillis = func() int64 { return 1700000000000 }
	return New(db), db
}

func TestApplyBuyCreatesPosition(t *testing.T) {
	m, _ := setup(t)
	id, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40)
	if err != nil {
		t.Fatalf("ApplyBuy: %v", err)
	}
	if id == 0 {
		t.Fatal("expected nonzero position id")
	}
	p, err := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if err != nil {
		t.Fatalf("GetOpen: %v", err)
	}
	if p == nil {
		t.Fatal("expected open position")
	}
	if p.FilledBuyCount != 10 {
		t.Errorf("FilledBuyCount = %v, want 10", p.FilledBuyCount)
	}
	if p.AvgEntryPrice != 0.40 {
		t.Errorf("AvgEntryPrice = %v, want 0.40", p.AvgEntryPrice)
	}
	if p.Status != store.PositionStatusOpen {
		t.Errorf("Status = %v, want open", p.Status)
	}
}

func TestApplyBuyAggregatesAndReweights(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.50); err != nil {
		t.Fatal(err)
	}
	p, _ := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p.FilledBuyCount != 20 {
		t.Errorf("FilledBuyCount = %v, want 20", p.FilledBuyCount)
	}
	if p.AvgEntryPrice != 0.45 {
		t.Errorf("AvgEntryPrice = %v, want 0.45", p.AvgEntryPrice)
	}
}

func TestApplySellRejectsNoOpen(t *testing.T) {
	m, _ := setup(t)
	_, _, _, err := m.ApplySell(context.Background(), "E1", "M1", "setpoint", false, 10, 0.60)
	if err != ErrNoOpenPosition {
		t.Errorf("expected ErrNoOpenPosition, got %v", err)
	}
}

func TestApplySellRejectsOversized(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := m.ApplySell(context.Background(), "E1", "M1", "setpoint", false, 15, 0.60)
	if err == nil {
		t.Fatal("expected error on oversized sell, got nil")
	}
}

func TestApplySellComputesRealizedPnL(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	id, pnl, remaining, err := m.ApplySell(context.Background(), "E1", "M1", "setpoint", false, 10, 0.60)
	if err != nil {
		t.Fatalf("ApplySell: %v", err)
	}
	if id == 0 {
		t.Error("expected position id")
	}
	// (0.60 - 0.40) * 10 * 100 = 200 cents
	if pnl != 200 {
		t.Errorf("realized pnl = %v, want 200", pnl)
	}
	if remaining != 0 {
		t.Errorf("remaining = %v, want 0", remaining)
	}
	// Position should be closed now.
	p, _ := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p != nil {
		t.Errorf("expected position closed (no open), got %+v", p)
	}
}

func TestApplySellPartialKeepsOpen(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	_, pnl, remaining, err := m.ApplySell(context.Background(), "E1", "M1", "setpoint", false, 4, 0.60)
	if err != nil {
		t.Fatalf("ApplySell: %v", err)
	}
	// (0.60 - 0.40) * 4 * 100 = 80 cents
	if pnl != 80 {
		t.Errorf("pnl = %v, want 80", pnl)
	}
	if remaining != 6 {
		t.Errorf("remaining = %v, want 6", remaining)
	}
	p, _ := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected position still open")
	}
	if p.FilledSellCount != 4 {
		t.Errorf("FilledSellCount = %v, want 4", p.FilledSellCount)
	}
	if p.AvgExitPrice != 0.60 {
		t.Errorf("AvgExitPrice = %v, want 0.60", p.AvgExitPrice)
	}
	if p.RealizedPNLCents != 80 {
		t.Errorf("RealizedPNLCents = %v, want 80", p.RealizedPNLCents)
	}
}

func TestSettleWon(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	id, pnl, remaining, err := m.Settle(context.Background(), "E1", "M1", "setpoint", false, true)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if id == 0 {
		t.Error("expected position id")
	}
	// 10 contracts remaining, won => (1 - 0.40) * 10 * 100 = 600
	if pnl != 600 {
		t.Errorf("settlement pnl = %v, want 600", pnl)
	}
	if remaining != 10 {
		t.Errorf("remaining = %v, want 10", remaining)
	}
}

func TestSettleLost(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	_, pnl, _, err := m.Settle(context.Background(), "E1", "M1", "setpoint", false, false)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// lost => -0.40 * 10 * 100 = -400
	if pnl != -400 {
		t.Errorf("settlement pnl = %v, want -400", pnl)
	}
}

func TestSettleAfterPartialSell(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.ApplySell(context.Background(), "E1", "M1", "setpoint", false, 4, 0.60); err != nil {
		t.Fatal(err)
	}
	_, settlePnl, remaining, err := m.Settle(context.Background(), "E1", "M1", "setpoint", false, true)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// 6 remaining, won => (1 - 0.40) * 6 * 100 = 360
	if settlePnl != 360 {
		t.Errorf("settlement pnl = %v, want 360", settlePnl)
	}
	if remaining != 6 {
		t.Errorf("remaining = %v, want 6", remaining)
	}
}

func TestSettleNoPosition(t *testing.T) {
	m, _ := setup(t)
	id, pnl, remaining, err := m.Settle(context.Background(), "E1", "M1", "setpoint", false, true)
	if err != nil {
		t.Errorf("expected nil error for no position, got %v", err)
	}
	if id != 0 || pnl != 0 || remaining != 0 {
		t.Errorf("expected zeros, got id=%d pnl=%d remaining=%v", id, pnl, remaining)
	}
}

func TestSettleIdempotent(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.Settle(context.Background(), "E1", "M1", "setpoint", false, true); err != nil {
		t.Fatal(err)
	}
	// Second settle should be no-op.
	_, pnl, _, err := m.Settle(context.Background(), "E1", "M1", "setpoint", false, true)
	if err != nil {
		t.Fatalf("second Settle: %v", err)
	}
	if pnl != 0 {
		t.Errorf("second settle pnl = %v, want 0 (idempotent)", pnl)
	}
}

func TestReopenAfterClose(t *testing.T) {
	m, _ := setup(t)
	// Buy 10, sell 10 (closed), buy 5 again -> should reopen.
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.ApplySell(context.Background(), "E1", "M1", "setpoint", false, 10, 0.60); err != nil {
		t.Fatal(err)
	}
	p, _ := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p != nil {
		t.Fatal("expected no open position after close")
	}
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 5, 0.30); err != nil {
		t.Fatal(err)
	}
	p, _ = m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected reopened position")
	}
	if p.FilledBuyCount != 15 {
		t.Errorf("FilledBuyCount = %v, want 15 (cumulative)", p.FilledBuyCount)
	}
	if p.FilledSellCount != 10 {
		t.Errorf("FilledSellCount = %v, want 10", p.FilledSellCount)
	}
	// Avg entry reweighted: (10*0.40 + 5*0.30) / 15 = 0.3667
	if p.AvgEntryPrice < 0.366 || p.AvgEntryPrice > 0.367 {
		t.Errorf("AvgEntryPrice = %v, want ~0.3667", p.AvgEntryPrice)
	}
	if p.Status != store.PositionStatusOpen {
		t.Errorf("Status = %v, want open", p.Status)
	}
}

func TestRealAndPaperSeparate(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", false, 10, 0.40); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplyBuy(context.Background(), "E1", "M1", "setpoint", true, 5, 0.50); err != nil {
		t.Fatal(err)
	}
	paperP, _ := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	realP, _ := m.GetOpenForStrategy(context.Background(), "M1", "setpoint", true)
	if paperP == nil || realP == nil {
		t.Fatal("expected both paper and real positions")
	}
	if paperP.ID == realP.ID {
		t.Error("paper and real should be separate positions")
	}
	if paperP.FilledBuyCount != 10 || realP.FilledBuyCount != 5 {
		t.Errorf("paper=%v real=%v", paperP.FilledBuyCount, realP.FilledBuyCount)
	}
}
