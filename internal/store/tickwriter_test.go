package store

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// TestRunFlushesOnCancelledContext ingests 5 ticks, cancels ctx, waits for
// Run to return, and asserts all 5 rows landed in ticks despite the parent
// ctx being cancelled — the terminal flush must use a detached context.
func TestRunFlushesOnCancelledContext(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping Postgres-backed tickwriter test")
	}

	// Reuse the schema-isolated helper from testdb_test.go.
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	w := db.NewTickWriter(100, 1000, slog.Default())

	// Ingest 5 ticks before starting the writer so they sit in the channel.
	for i := 0; i < 5; i++ {
		w.Ingest(Tick{
			TS:           int64(1700000000000 + i*1000),
			RecvTS:       int64(1700000000001 + i*1000),
			MarketTicker: "FLUSHTEST",
			MsgType:      "ticker",
			Payload:      "{}",
		})
	}

	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = w.Run(ctx)
	}()

	// Give the writer a moment to drain the channel into its in-memory batch,
	// then cancel. The batch won't have hit the batchSize=100 trigger, so the
	// only path to persistence is the terminal flush.
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	if runErr != context.Canceled {
		t.Fatalf("Run err = %v, want context.Canceled", runErr)
	}

	// Assert all 5 ticks persisted.
	var n int64
	if err := db.GormDB().Raw(`SELECT count(*) FROM ticks WHERE market_ticker = 'FLUSHTEST'`).Scan(&n).Error; err != nil {
		t.Fatalf("count ticks: %v", err)
	}
	if n != 5 {
		t.Fatalf("ticks persisted = %d, want 5 (terminal flush must land on cancelled ctx)", n)
	}
}
