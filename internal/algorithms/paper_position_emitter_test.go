package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/positions"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// testLogger returns a quiet slog logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&devNull{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// paperTestDB opens a fresh sqlite DB with the Position + Order tables
// for paper-path integration testing. Each test gets its own DB via
// unique DSN to avoid cross-test contamination.
func paperTestDB(t *testing.T) *store.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&store.Position{}, &store.Order{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.NewFromGorm(gdb, testLogger())
}

// nopEmitter is an OrderEmitter that records everything forwarded to it.
// Used as the inner of PaperPositionEmitter to verify what reaches the
// persistence layer (TickWriterEmitter in production).
type nopEmitter struct {
	forwarded []store.Order
}

func (n *nopEmitter) EmitOrder(o store.Order) bool {
	n.forwarded = append(n.forwarded, o)
	return true
}

// buyOrder builds a paper buy order with sensible defaults.
func buyOrder(market, strategy string, size float64, price float64) store.Order {
	return store.Order{
		MatchTicker:   "E1",
		MarketTicker:  market,
		Strategy:      strategy,
		Action:        "buy",
		Side:          store.OrderSideOpen,
		SuggestedSize: size,
		MarketPrice:   price,
	}
}

// sellOrder builds a paper sell order.
func sellOrder(market, strategy string, size float64, price float64) store.Order {
	return store.Order{
		MatchTicker:   "E1",
		MarketTicker:  market,
		Strategy:      strategy,
		Action:        "sell",
		Side:          store.OrderSideClose,
		SuggestedSize: size,
		MarketPrice:   price,
	}
}

// TestPaperBuyCreatesPosition verifies a paper buy flows through the
// emitter, gets forwarded to inner, and creates an open position row.
func TestPaperBuyCreatesPosition(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	o := buyOrder("M1", "setpoint", 10, 0.40)
	if !emitter.EmitOrder(o) {
		t.Fatal("buy dropped")
	}
	if len(inner.forwarded) != 1 {
		t.Fatalf("inner received %d orders, want 1", len(inner.forwarded))
	}
	if inner.forwarded[0].Action != "buy" {
		t.Errorf("forwarded action = %v, want buy", inner.forwarded[0].Action)
	}
	if inner.forwarded[0].PositionID == nil {
		t.Error("forwarded order missing position_id")
	}

	mgr := positions.New(db.GormDB())
	p, err := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if err != nil {
		t.Fatalf("GetOpen: %v", err)
	}
	if p == nil {
		t.Fatal("expected open position after buy")
	}
	if p.FilledBuyCount != 10 {
		t.Errorf("FilledBuyCount = %v, want 10", p.FilledBuyCount)
	}
	if p.AvgEntryPrice != 0.40 {
		t.Errorf("AvgEntryPrice = %v, want 0.40", p.AvgEntryPrice)
	}
}

// TestPaperSellRejectsNoOpen verifies a paper sell with no open position
// is dropped — no naked shorts, no forward to inner.
func TestPaperSellRejectsNoOpen(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	o := sellOrder("M1", "setpoint", 10, 0.60)
	if emitter.EmitOrder(o) {
		t.Error("sell should be dropped (no open position)")
	}
	if len(inner.forwarded) != 0 {
		t.Errorf("inner received %d orders, want 0 (sell dropped)", len(inner.forwarded))
	}
}

// TestPaperBuyThenSell verifies the full cycle: buy creates position,
// sell closes it, realized PnL is correct, position status flips to closed.
func TestPaperBuyThenSell(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	// Buy 10 @ 0.40
	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 10, 0.40)) {
		t.Fatal("buy dropped")
	}
	// Sell 10 @ 0.60 — should close position, realize (0.60-0.40)*10*100 = 200c
	if !emitter.EmitOrder(sellOrder("M1", "setpoint", 10, 0.60)) {
		t.Fatal("sell dropped")
	}
	if len(inner.forwarded) != 2 {
		t.Fatalf("inner received %d orders, want 2", len(inner.forwarded))
	}

	mgr := positions.New(db.GormDB())
	p, err := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if err != nil {
		t.Fatalf("GetOpen: %v", err)
	}
	if p != nil {
		t.Errorf("expected no open position after full close, got %+v", p)
	}

	// Verify position row is closed with correct realized PnL.
	var pos store.Position
	if err := db.GormDB().Where("market_ticker = ? AND strategy = ?", "M1", "setpoint").
		First(&pos).Error; err != nil {
		t.Fatalf("fetch position: %v", err)
	}
	if pos.Status != store.PositionStatusClosed {
		t.Errorf("Status = %v, want closed", pos.Status)
	}
	if pos.RealizedPNLCents != 200 {
		t.Errorf("RealizedPNLCents = %v, want 200", pos.RealizedPNLCents)
	}
	if pos.FilledBuyCount != 10 || pos.FilledSellCount != 10 {
		t.Errorf("buy=%v sell=%v, want 10/10", pos.FilledBuyCount, pos.FilledSellCount)
	}
}

// TestPaperPartialSellKeepsOpen verifies partial sell: position stays
// open, realized PnL reflects only the sold portion.
func TestPaperPartialSellKeepsOpen(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 10, 0.40)) {
		t.Fatal("buy dropped")
	}
	// Sell 4 @ 0.60 — partial close, 6 remaining
	if !emitter.EmitOrder(sellOrder("M1", "setpoint", 4, 0.60)) {
		t.Fatal("partial sell dropped")
	}

	mgr := positions.New(db.GormDB())
	p, _ := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected position still open after partial sell")
	}
	if p.FilledSellCount != 4 {
		t.Errorf("FilledSellCount = %v, want 4", p.FilledSellCount)
	}
	if p.RealizedPNLCents != 80 {
		t.Errorf("RealizedPNLCents = %v, want 80", p.RealizedPNLCents)
	}
}

// TestPaperSellClampedToOpen verifies a sell larger than open contracts
// is rejected by the position manager (no naked shorts).
func TestPaperSellClampedToOpen(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 5, 0.40)) {
		t.Fatal("buy dropped")
	}
	// Try to sell 10 when only 5 open — should be rejected.
	if emitter.EmitOrder(sellOrder("M1", "setpoint", 10, 0.60)) {
		t.Error("oversized sell should be dropped")
	}
	if len(inner.forwarded) != 1 {
		t.Errorf("inner received %d orders, want 1 (only the buy)", len(inner.forwarded))
	}
}

// TestPaperMultipleBuysAggregate verifies multiple buys on same
// market+strategy aggregate into one position with reweighted avg entry.
func TestPaperMultipleBuysAggregate(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 10, 0.40)) {
		t.Fatal("buy 1 dropped")
	}
	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 10, 0.50)) {
		t.Fatal("buy 2 dropped")
	}
	if len(inner.forwarded) != 2 {
		t.Fatalf("inner received %d, want 2", len(inner.forwarded))
	}

	mgr := positions.New(db.GormDB())
	p, _ := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected open position")
	}
	if p.FilledBuyCount != 20 {
		t.Errorf("FilledBuyCount = %v, want 20", p.FilledBuyCount)
	}
	// (10*0.40 + 10*0.50) / 20 = 0.45
	if p.AvgEntryPrice != 0.45 {
		t.Errorf("AvgEntryPrice = %v, want 0.45", p.AvgEntryPrice)
	}
}

// TestPaperSettleRemaining verifies that after a partial sell, settling
// the market finalizes the remaining open contracts.
func TestPaperSettleRemaining(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	// Buy 10 @ 0.40, sell 4 @ 0.60 (realize 80c, 6 remaining)
	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 10, 0.40)) {
		t.Fatal("buy dropped")
	}
	if !emitter.EmitOrder(sellOrder("M1", "setpoint", 4, 0.60)) {
		t.Fatal("sell dropped")
	}

	// Settle as won — remaining 6 contracts pay $1 each.
	// Settlement PnL = (1 - 0.40) * 6 * 100 = 360c
	mgr := positions.New(db.GormDB())
	_, settlePnL, remaining, err := mgr.Settle(context.Background(), "E1", "M1", "setpoint", false, true)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if remaining != 6 {
		t.Errorf("remaining = %v, want 6", remaining)
	}
	if settlePnL != 360 {
		t.Errorf("settlement pnl = %v, want 360", settlePnL)
	}

	// Total realized = 80 (from sell) + 360 (from settle) = 440
	var pos store.Position
	if err := db.GormDB().Where("market_ticker = ? AND strategy = ?", "M1", "setpoint").
		First(&pos).Error; err != nil {
		t.Fatalf("fetch position: %v", err)
	}
	if pos.Status != store.PositionStatusSettled {
		t.Errorf("Status = %v, want settled", pos.Status)
	}
	if pos.RealizedPNLCents != 440 {
		t.Errorf("total RealizedPNLCents = %v, want 440 (80 sell + 360 settle)", pos.RealizedPNLCents)
	}
}

// TestPaperReopenAfterClose verifies that buying again after a full
// close reopens the position (cumulative counts, reweighted avg).
func TestPaperReopenAfterClose(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	// Buy 10, sell 10 (closed), buy 5 again
	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 10, 0.40)) {
		t.Fatal("buy 1 dropped")
	}
	if !emitter.EmitOrder(sellOrder("M1", "setpoint", 10, 0.60)) {
		t.Fatal("sell dropped")
	}
	if !emitter.EmitOrder(buyOrder("M1", "setpoint", 5, 0.30)) {
		t.Fatal("buy 2 dropped")
	}

	mgr := positions.New(db.GormDB())
	p, _ := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected reopened position")
	}
	if p.FilledBuyCount != 15 {
		t.Errorf("FilledBuyCount = %v, want 15 (cumulative)", p.FilledBuyCount)
	}
	if p.FilledSellCount != 10 {
		t.Errorf("FilledSellCount = %v, want 10", p.FilledSellCount)
	}
	// Avg entry: (10*0.40 + 5*0.30) / 15 = 0.3667
	if p.AvgEntryPrice < 0.366 || p.AvgEntryPrice > 0.367 {
		t.Errorf("AvgEntryPrice = %v, want ~0.3667", p.AvgEntryPrice)
	}
}

// TestPaperRealAndPaperSeparate verifies real and paper positions for
// the same market+strategy are tracked independently.
func TestPaperRealAndPaperSeparate(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	// Paper buy
	paperBuy := buyOrder("M1", "setpoint", 10, 0.40)
	paperBuy.IsReal = false
	if !emitter.EmitOrder(paperBuy) {
		t.Fatal("paper buy dropped")
	}
	// Real buy — PaperPositionEmitter always passes isReal=false to
	// positions.Manager. This test verifies paper path only tracks paper.
	// Real positions are tracked by KalshiOrderEmitter, not paper path.
	// So emitting a "real" order through paper path still creates a paper position.
	// This is correct: paper path is for simulated orders only.
	realBuy := buyOrder("M1", "setpoint", 5, 0.50)
	realBuy.IsReal = true
	if !emitter.EmitOrder(realBuy) {
		t.Fatal("real buy dropped")
	}

	// Both should land as paper positions (isReal=false) since paper path
	// always uses isReal=false in ApplyBuy. The isReal field on the order
	// is for the orders table, not position tracking.
	mgr := positions.New(db.GormDB())
	p, _ := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected paper position")
	}
	if p.FilledBuyCount != 15 {
		t.Errorf("FilledBuyCount = %v, want 15 (both buys aggregated as paper)", p.FilledBuyCount)
	}
}

// TestPaperLegacyOrderNoSide verifies an order with empty Side defaults
// to open (buy path) for backward compat with existing strategies.
func TestPaperLegacyOrderNoSide(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	// Legacy order: Action="buy", Side="" (existing strategies don't set Side)
	legacy := buyOrder("M1", "setpoint", 10, 0.40)
	legacy.Side = ""
	if !emitter.EmitOrder(legacy) {
		t.Fatal("legacy buy dropped")
	}
	if inner.forwarded[0].Side != store.OrderSideOpen {
		t.Errorf("forwarded side = %v, want open (normalized)", inner.forwarded[0].Side)
	}

	mgr := positions.New(db.GormDB())
	p, _ := mgr.GetOpenForStrategy(context.Background(), "M1", "setpoint", false)
	if p == nil {
		t.Fatal("expected position from legacy buy")
	}
}

// TestPaperSellZeroSize verifies a sell with zero size forwards without
// position update (edge case guard).
func TestPaperSellZeroSize(t *testing.T) {
	db := paperTestDB(t)
	inner := &nopEmitter{}
	emitter := NewPaperPositionEmitter(inner, db, testLogger())

	// Zero-size sell should forward without position update (no position to close).
	zeroSell := sellOrder("M1", "setpoint", 0, 0.60)
	if !emitter.EmitOrder(zeroSell) {
		t.Fatal("zero-size sell should forward")
	}
	if len(inner.forwarded) != 1 {
		t.Fatalf("inner received %d, want 1", len(inner.forwarded))
	}
}
