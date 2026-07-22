package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/ledger"
	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB opens a GORM connection to a per-test schema and creates the
// pool_ledger + pool_balance tables. Skips when TEST_DB_DSN is unset.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping Postgres-backed ledger tests")
	}

	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_ledger_" + hex.EncodeToString(b[:])

	{
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatalf("connect for schema create: %v", err)
		}
		if _, err := conn.Exec(context.Background(),
			fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
			_ = conn.Close(context.Background())
			t.Fatalf("create schema: %v", err)
		}
		_ = conn.Close(context.Background())
	}

	db, err := gorm.Open(postgres.Open(dsn+"&search_path="+schema), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	if err := db.AutoMigrate(&poolLedgerRow{}, &poolBalanceRow{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		_, _ = conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
		_ = conn.Close(context.Background())
	})

	return db
}

func TestLedgerInsufficientBalance(t *testing.T) {
	db := testDB(t)
	repo := NewLedgerRepo(db)
	ctx := context.Background()

	if err := repo.InitBalance(ctx, 1000); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Hold within balance → ok.
	if err := repo.HoldForOrder(ctx, 1, 800); err != nil {
		t.Fatalf("hold 800: %v", err)
	}

	// Hold exceeding balance → ErrInsufficientBalance.
	err := repo.HoldForOrder(ctx, 2, 300)
	if err == nil {
		t.Fatal("hold 300 with 200 balance: expected error, got nil")
	}
	if err != ledger.ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}

	bal, _ := repo.GetBalance(ctx)
	if bal != 200 {
		t.Errorf("balance = %d, want 200", bal)
	}
}

func TestLedgerConcurrentHolds(t *testing.T) {
	db := testDB(t)
	repo := NewLedgerRepo(db)
	ctx := context.Background()

	// Start with 1000 cents. 20 goroutines try to hold 100 each.
	// Exactly 10 should succeed (1000/100 = 10).
	if err := repo.InitBalance(ctx, 1000); err != nil {
		t.Fatalf("init: %v", err)
	}

	var successCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			orderID := int64(idx + 1)
			if err := repo.HoldForOrder(ctx, orderID, 100); err == nil {
				successCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	got := successCount.Load()
	if got != 10 {
		t.Errorf("successful holds = %d, want 10", got)
	}

	bal, _ := repo.GetBalance(ctx)
	if bal != 0 {
		t.Errorf("balance = %d, want 0", bal)
	}
}

func TestLedgerInvariantChecker(t *testing.T) {
	db := testDB(t)
	repo := NewLedgerRepo(db)
	ctx := context.Background()

	if err := repo.InitBalance(ctx, 10000); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Deposit 5000 more.
	if err := repo.Deposit(ctx, 5000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Hold 3000 for order 1.
	if err := repo.HoldForOrder(ctx, 1, 3000); err != nil {
		t.Fatalf("hold: %v", err)
	}

	// Release 1000 (partial fill — unfilled remainder).
	if err := repo.ReleaseHold(ctx, 1, 1000); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Record fill cost of 2000 (the filled part).
	if err := repo.RecordFill(ctx, 1, 2000); err != nil {
		t.Fatalf("fill: %v", err)
	}

	// Settlement payout of 4000 (win).
	if err := repo.RecordSettlement(ctx, 1, 4000); err != nil {
		t.Fatalf("settlement: %v", err)
	}

	entries, err := repo.GetEntries(ctx)
	if err != nil {
		t.Fatalf("get entries: %v", err)
	}

	bal, _ := repo.GetBalance(ctx)

	// Balance: 10000 + 5000 - 3000 + 1000 - 2000 + 4000 = 15000
	if bal != 15000 {
		t.Errorf("balance = %d, want 15000", bal)
	}

	if err := ledger.CheckInvariants(entries, bal); err != nil {
		t.Errorf("invariant check failed: %v", err)
	}
}

func TestLedgerInvariantCheckerCatchesCorruption(t *testing.T) {
	entries := []ledger.Entry{
		{ID: 1, TS: 1, EntryType: ledger.EntryDeposit, AmountCents: 10000},
		{ID: 2, TS: 2, EntryType: ledger.EntryOrderHold, AmountCents: -3000, OrderID: int64Ptr(1)},
		{ID: 3, TS: 3, EntryType: ledger.EntryHoldRelease, AmountCents: 5000, OrderID: int64Ptr(1)}, // BUG: release > hold
	}

	// Sum = 10000 - 3000 + 5000 = 12000
	err := ledger.CheckInvariants(entries, 12000)
	if err == nil {
		t.Fatal("expected invariant error for release > hold, got nil")
	}
}

func TestLedgerInvariantSumMismatch(t *testing.T) {
	entries := []ledger.Entry{
		{ID: 1, TS: 1, EntryType: ledger.EntryDeposit, AmountCents: 10000},
	}
	// Sum = 10000 but we claim balance = 9999
	err := ledger.CheckInvariants(entries, 9999)
	if err == nil {
		t.Fatal("expected sum mismatch error, got nil")
	}
}

func TestLedgerReleaseAndFillFlow(t *testing.T) {
	db := testDB(t)
	repo := NewLedgerRepo(db)
	ctx := context.Background()

	if err := repo.InitBalance(ctx, 5000); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Hold 2000 for order 1 (10 contracts × 200 cents).
	if err := repo.HoldForOrder(ctx, 1, 2000); err != nil {
		t.Fatalf("hold: %v", err)
	}

	bal, _ := repo.GetBalance(ctx)
	if bal != 3000 {
		t.Fatalf("after hold: balance = %d, want 3000", bal)
	}

	// Partial fill: 6 contracts filled (cost 1200), release remainder 800.
	if err := repo.RecordFill(ctx, 1, 1200); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if err := repo.ReleaseHold(ctx, 1, 800); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Balance: 5000 - 2000 - 1200 + 800 = 2600
	bal, _ = repo.GetBalance(ctx)
	if bal != 2600 {
		t.Errorf("balance = %d, want 2600", bal)
	}

	entries, _ := repo.GetEntries(ctx)
	if err := ledger.CheckInvariants(entries, bal); err != nil {
		t.Errorf("invariant check: %v", err)
	}
}

func int64Ptr(v int64) *int64 { return &v }

// TestLedgerDomainRules tests the pure domain functions without DB.
func TestLedgerDomainRules(t *testing.T) {
	// Hold
	bal, err := ledger.Hold(1000, 300)
	if err != nil || bal != 700 {
		t.Errorf("Hold(1000, 300) = (%d, %v), want (700, nil)", bal, err)
	}

	_, err = ledger.Hold(100, 200)
	if err != ledger.ErrInsufficientBalance {
		t.Errorf("Hold(100, 200) err = %v, want ErrInsufficientBalance", err)
	}

	// ReleaseHold
	if bal := ledger.ReleaseHold(700, 100); bal != 800 {
		t.Errorf("ReleaseHold(700, 100) = %d, want 800", bal)
	}

	// RecordFill
	if bal := ledger.RecordFill(800, 200); bal != 600 {
		t.Errorf("RecordFill(800, 200) = %d, want 600", bal)
	}

	// RecordSettlement
	if bal := ledger.RecordSettlement(600, 500); bal != 1100 {
		t.Errorf("RecordSettlement(600, 500) = %d, want 1100", bal)
	}

	// Deposit
	if bal := ledger.Deposit(1100, 400); bal != 1500 {
		t.Errorf("Deposit(1100, 400) = %d, want 1500", bal)
	}

	// Withdraw
	bal, err = ledger.Withdraw(1500, 300)
	if err != nil || bal != 1200 {
		t.Errorf("Withdraw(1500, 300) = (%d, %v), want (1200, nil)", bal, err)
	}

	_, err = ledger.Withdraw(100, 200)
	if err != ledger.ErrInsufficientBalance {
		t.Errorf("Withdraw(100, 200) err = %v, want ErrInsufficientBalance", err)
	}
}

// Ensure time is imported for the repo.
var _ = time.Now
