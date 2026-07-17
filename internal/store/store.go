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
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

// DB is the SQLite store for tennis match data. Single-writer architecture.
type DB struct {
	db  *sql.DB
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
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer — SQLite serializes writes regardless
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)

	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db, log: log}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Add columns to lifecycle_events for pre-existing DBs.
	// Check PRAGMA table_info instead of matching driver error strings.
	if err := addColumnIfMissing(ctx, db, "lifecycle_events", "open_ts", "INTEGER"); err != nil {
		return fmt.Errorf("migrate open_ts: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "lifecycle_events", "settlement_value", "TEXT"); err != nil {
		return fmt.Errorf("migrate settlement_value: %w", err)
	}
	if err := addColumnIfMissing(ctx, db, "orders", "strategy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate orders.strategy: %w", err)
	}

	return nil
}

// addColumnIfMissing adds a column to a table if it does not already exist.
// Uses PRAGMA table_info so it does not depend on driver error message format.
func addColumnIfMissing(ctx context.Context, db *sql.DB, table, column, decl string) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	return err
}

// nowMillis returns current time in milliseconds. Centralized for consistency.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
