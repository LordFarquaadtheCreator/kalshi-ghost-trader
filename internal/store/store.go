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

	// Single writer
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxIdleTime(0)

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

// Migrate runs schema migration (AutoMigrate + SQL migrations).
// Must be called once at app startup after New.
func (d *DB) Migrate() error {
	if err := d.db.AutoMigrate(allModels...); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	if err := d.RunAllMigrations(); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// nowMillis returns current time in milliseconds. Centralized for consistency.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
