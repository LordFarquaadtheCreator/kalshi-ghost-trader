// Package store implements the PostgreSQL persistence layer for the ghost-trader service.
//
// The DB struct wraps a GORM connection to PostgreSQL. Writes are serialized
// through TickWriter, a dedicated goroutine that batches inserts across
// buffered channels: ticks, orderbook events, lifecycle events, and event
// lifecycle events. Non-blocking ingest drops on full buffer with a warning log.
//
// Tables:
//   - events — tennis match events (PK: event_ticker)
//   - markets — two per event, one per player (PK: market_ticker)
//   - ticks — every WS ticker/trade message with raw JSON payload (no FK — log table)
//   - orderbook_events — orderbook snapshots and deltas (no FK — log table)
//   - lifecycle_events — market_lifecycle_v2 WS events (no FK — log table)
//   - event_lifecycle_events — event_lifecycle WS messages (no FK — log table)
//   - scan_runs — scanner audit log
//
// Log tables intentionally have no foreign keys — WS messages can arrive
// before the scanner stores the parent market/event, and rejecting them
// would cause data loss. Orphan cleanup is handled by [DB.CleanOrphans].
//
// Cascade triggers on events and markets handle child row deletion via
// PL/pgSQL trigger functions. The janitor ([DB.CleanOrphans], [DB.AdoptOrphans])
// removes orphaned rows older than 6 hours and attempts to parent orphan
// event lifecycle events by creating missing event records.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the PostgreSQL store for tennis match data.
type DB struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewFromGorm wraps an existing *gorm.DB as a *DB. Used by tests that
// want sqlite instead of postgres. Production code should use New.
func NewFromGorm(db *gorm.DB, log *slog.Logger) *DB {
	return &DB{db: db, log: log}
}

// New opens the PostgreSQL database using the provided DSN.
func New(ctx context.Context, dsn string, log *slog.Logger) (*DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return &DB{db: db, log: log}, nil
}

// NewDashboardDB opens a dedicated PostgreSQL pool for dashboard reads.
// statement_timeout=5s prevents slow queries from blocking the pool;
// MaxOpenConns=5 keeps the dashboard from starving the writer pool.
// The dashboard queries pre-computed data — no recompute on page load.
func NewDashboardDB(ctx context.Context, dsn string, log *slog.Logger) (*DB, error) {
	// Quote the options value — the space between -c and statement_timeout=5s
	// would break the key=value DSN parser without single quotes.
	dashboardDSN := dsn + " options='-c statement_timeout=5s'"
	db, err := gorm.Open(postgres.Open(dashboardDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open dashboard postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying dashboard sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(5)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
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
	if err := d.db.AutoMigrate(AllModels...); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	// Cascade trigger functions (Postgres uses PL/pgSQL, not inline trigger bodies).
	d.db.Exec(`CREATE OR REPLACE FUNCTION cascade_delete_market() RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM ticks WHERE market_ticker = OLD.market_ticker;
    DELETE FROM orderbook_events WHERE market_ticker = OLD.market_ticker;
    DELETE FROM lifecycle_events WHERE market_ticker = OLD.market_ticker;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql`)
	d.db.Exec(`DROP TRIGGER IF EXISTS trg_markets_delete_cascade ON markets`)
	d.db.Exec(`CREATE TRIGGER trg_markets_delete_cascade
AFTER DELETE ON markets
FOR EACH ROW EXECUTE FUNCTION cascade_delete_market()`)

	d.db.Exec(`CREATE OR REPLACE FUNCTION cascade_delete_event() RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM markets WHERE event_ticker = OLD.event_ticker;
    DELETE FROM event_lifecycle_events WHERE event_ticker = OLD.event_ticker;
    DELETE FROM orders WHERE match_ticker = OLD.event_ticker;
    DELETE FROM positions WHERE match_ticker = OLD.event_ticker;
    DELETE FROM fired_events WHERE event_ticker = OLD.event_ticker;
    DELETE FROM points WHERE match_ticker = OLD.event_ticker;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql`)
	d.db.Exec(`DROP TRIGGER IF EXISTS trg_events_delete_cascade ON events`)
	d.db.Exec(`CREATE TRIGGER trg_events_delete_cascade
AFTER DELETE ON events
FOR EACH ROW EXECUTE FUNCTION cascade_delete_event()`)

	if err := d.RunAllMigrations(); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// nowMillis returns current time in milliseconds. Centralized for consistency.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
