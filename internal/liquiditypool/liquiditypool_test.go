package liquiditypool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/jackc/pgx/v5"
)

// testDB opens a Postgres-backed *store.DB isolated per test via a unique
// schema. Skips when TEST_DB_DSN is unset. Mirrors the helper in
// internal/store/testdb_test.go.
func testDB(t *testing.T) *store.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping Postgres-backed liquiditypool tests")
	}

	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_lp_" + hex.EncodeToString(b[:])

	{
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatalf("connect for schema create: %v", err)
		}
		if _, err := conn.Exec(context.Background(),
			fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
			conn.Close(context.Background())
			t.Fatalf("create schema: %v", err)
		}
		conn.Close(context.Background())
	}

	db, err := store.New(context.Background(), dsn+" search_path="+schema, slog.Default())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatalf("Migrate: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Logf("connect for schema drop: %v", err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(),
			fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema)); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	return db
}

// seedPool resets the singleton pool row to the given balance. Uses Reset
// (not Init) because migration 0002 already seeds the row with 100000 cents
// and Init's OnConflict DoNothing would leave that seed in place.
func seedPool(t *testing.T, db *store.DB, balanceCents int64) {
	t.Helper()
	if err := Init(context.Background(), db.GormDB(), balanceCents); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := Reset(context.Background(), db.GormDB(), balanceCents); err != nil {
		t.Fatalf("Reset: %v", err)
	}
}

func TestDeductInsufficientBalance(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	seedPool(t, db, 100)

	newBalance, err := Deduct(ctx, db.GormDB(), 150)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("Deduct(150) err = %v, want ErrInsufficientBalance", err)
	}
	if newBalance != 0 {
		t.Fatalf("Deduct(150) newBalance = %d, want 0 on failure", newBalance)
	}

	lp, err := Get(ctx, db.GormDB())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if lp.BalanceCents != 100 {
		t.Fatalf("balance after failed deduct = %d, want 100 (unchanged)", lp.BalanceCents)
	}
}

func TestDeductConcurrent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	seedPool(t, db, 1000)

	const goroutines = 20
	const spend = int64(100)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var success, insufficient int64
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := Deduct(ctx, db.GormDB(), spend)
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, ErrInsufficientBalance):
				atomic.AddInt64(&insufficient, 1)
			default:
				t.Errorf("Deduct returned unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()

	if success != 10 {
		t.Fatalf("successes = %d, want 10", success)
	}
	if insufficient != 10 {
		t.Fatalf("insufficient = %d, want 10", insufficient)
	}

	lp, err := Get(ctx, db.GormDB())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if lp.BalanceCents != 0 {
		t.Fatalf("final balance = %d, want 0", lp.BalanceCents)
	}
}
