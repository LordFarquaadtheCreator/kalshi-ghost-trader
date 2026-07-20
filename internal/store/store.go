// Package store implements the SQLite persistence layer for the ghost-trader service.
//
// The DB struct wraps a single SQLite connection configured with WAL mode,
// synchronous=NORMAL, and a 64MB page cache. A single-writer architecture
// is enforced via MaxOpenConns=1 — all writes are serialized.
//
// Data is ingested through TickWriter, a dedicated goroutine that batches
// inserts across four buffered channels: ticks, orderbook events, lifecycle
// events, and event lifecycle events. Non-blocking ingest drops on full
// buffer with a warning log.
//
// Tables:
//   - events — tennis match events (PK: event_ticker)
//   - markets — two per event, one per player (PK: market_ticker, FK: event_ticker)
//   - ticks — every WS ticker/trade message with raw JSON payload (no FK — log table)
//   - orderbook_events — orderbook snapshots and deltas (no FK — log table)
//   - lifecycle_events — market_lifecycle_v2 WS events (no FK — log table)
//   - event_lifecycle_events — event_lifecycle WS messages (no FK — log table)
//   - scan_runs — scanner audit log
//
// Log tables (ticks, orderbook_events, lifecycle_events, event_lifecycle_events)
// intentionally have no foreign keys — WS messages can arrive before
// the scanner stores the parent market/event, and rejecting them would cause
// data loss. Orphan cleanup is handled by [DB.CleanOrphans].
//
// Cascade triggers on events and markets handle child row deletion when a
// market or event is removed. The janitor ([DB.CleanOrphans], [DB.AdoptOrphans])
// removes orphaned rows older than 6 hours and attempts to parent orphan
// event lifecycle events by creating missing event records.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the SQLite store for tennis match data. Single-writer architecture.
type DB struct {
	db  *gorm.DB
	log *slog.Logger
}

// New opens the SQLite database with WAL mode and tuned PRAGMAs.
// PRAGMAs go in the DSN so every pooled connection gets them.
func New(ctx context.Context, path string, log *slog.Logger) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&"+
			"_pragma=busy_timeout(5000)&_pragma=cache_size(-64000)&"+
			"_pragma=temp_store(MEMORY)&_pragma=foreign_keys(ON)",
		path,
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Single writer — SQLite serializes writes regardless
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxIdleTime(0)

	if err := migrate(ctx, db); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return &DB{db: db, log: log}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// DB returns the underlying *gorm.DB for use by backtest engine and other consumers.
func (d *DB) GormDB() *gorm.DB {
	return d.db
}

func migrate(ctx context.Context, db *gorm.DB) error {
	// AutoMigrate creates tables from struct definitions. Idempotent.
	if err := db.AutoMigrate(
		&Event{}, &Market{}, &Tick{}, &OrderbookEvent{},
		&LifecycleEvent{}, &EventLifecycleEvent{}, &Order{},
		&ScanRun{}, &FiredEvent{}, &Point{}, &KalshiScore{},
		&AppConfigKV{}, &LiquidityPool{}, &StrategyConfigEntry{},
		&TriggerRange{}, &FlashscoreMatch{},
	); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	// Triggers and custom indexes can't be done via AutoMigrate.
	// Run the DDL for triggers and indexes only (tables already created above).
	if err := db.Exec(triggerDDL).Error; err != nil {
		return fmt.Errorf("migrate triggers: %w", err)
	}

	// Add columns to lifecycle_events for pre-existing DBs.
	if err := addColumnIfMissing(ctx, db, "lifecycle_events", "open_ts", "INTEGER"); err != nil {
		return fmt.Errorf("migrate open_ts: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "lifecycle_events", "settlement_value", "TEXT"); err != nil {
		return fmt.Errorf("migrate settlement_value: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "strategy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate orders.strategy: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "bankroll", "REAL NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate orders.bankroll: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "kelly_fraction", "REAL NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate orders.kelly_fraction: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "is_real", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate orders.is_real: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "kalshi_order_id", "TEXT"); err != nil {
		return fmt.Errorf("migrate orders.kalshi_order_id: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "fill_count", "REAL"); err != nil {
		return fmt.Errorf("migrate orders.fill_count: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "order_status", "TEXT"); err != nil {
		return fmt.Errorf("migrate orders.order_status: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "resolved_pnl_cents", "INTEGER"); err != nil {
		return fmt.Errorf("migrate orders.resolved_pnl_cents: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "pool_balance_before_cents", "INTEGER"); err != nil {
		return fmt.Errorf("migrate orders.pool_balance_before_cents: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "pool_balance_after_cents", "INTEGER"); err != nil {
		return fmt.Errorf("migrate orders.pool_balance_after_cents: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "match_title", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate orders.match_title: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "player_name", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate orders.player_name: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "unfilled_refunded_cents", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate orders.unfilled_refunded_cents: %w", err)
	}

	// Create idx_orders_real after is_real column is guaranteed present.
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_orders_real ON orders(is_real) WHERE is_real = 1").Error; err != nil {
		return fmt.Errorf("migrate idx_orders_real: %w", err)
	}
	// Keyset pagination index for /api/orders. Idempotent.
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_orders_ts_id ON orders(ts DESC, id DESC)").Error; err != nil {
		return fmt.Errorf("migrate idx_orders_ts_id: %w", err)
	}

	return nil
}

// addColumnIfMissing adds a column to a table if it does not already exist.
// Uses PRAGMA table_info so it does not depend on driver error message format.
func addColumnIfMissing(ctx context.Context, db *gorm.DB, table, column, decl string) error {
	var count int64
	if err := db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", table), column).Scan(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl)).Error
}

// nowMillis returns current time in milliseconds. Centralized for consistency.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
