package signal

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

type testEnv struct {
	db     *store.DB
	tw     *store.TickWriter
	ctx    context.Context
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.New(context.Background(), filepath.Join(dir, "test.db"), slog.Default())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	tw := db.NewTickWriter(100, 50, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tw.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		db.Close()
	})
	return &testEnv{db: db, tw: tw, ctx: ctx, cancel: cancel, wg: &wg}
}

func (e *testEnv) flushAndQueryOrders(t *testing.T) []store.Order {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	orders, err := e.db.GetOrders(context.Background())
	if err != nil {
		t.Fatalf("GetOrders: %v", err)
	}
	return orders
}
