package store

import (
	"context"
	"log/slog"
	"time"
)

const orphanAgeThreshold = 6 * time.Hour

// CleanOrphans removes child rows that reference parents that don't exist
// and are older than the age threshold (to avoid deleting rows whose parent
// might still be in-flight from a race condition).
func (d *DB) CleanOrphans(ctx context.Context, log *slog.Logger) error {
	cutoff := time.Now().Add(-orphanAgeThreshold).UnixMilli()

	// Orphan ticks: no parent market, and old enough
	res, err := d.db.ExecContext(ctx, `
DELETE FROM ticks WHERE id IN (
    SELECT t.id FROM ticks t
    WHERE t.ts < ?
    AND NOT EXISTS (SELECT 1 FROM markets m WHERE m.market_ticker = t.market_ticker)
)`, cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Info("cleaned orphan ticks", "count", n)
	}

	// Orphan orderbook_events
	res, err = d.db.ExecContext(ctx, `
DELETE FROM orderbook_events WHERE id IN (
    SELECT o.id FROM orderbook_events o
    WHERE o.ts < ?
    AND NOT EXISTS (SELECT 1 FROM markets m WHERE m.market_ticker = o.market_ticker)
)`, cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Info("cleaned orphan orderbook", "count", n)
	}

	// Orphan lifecycle_events
	res, err = d.db.ExecContext(ctx, `
DELETE FROM lifecycle_events WHERE id IN (
    SELECT l.id FROM lifecycle_events l
    WHERE l.ts < ?
    AND NOT EXISTS (SELECT 1 FROM markets m WHERE m.market_ticker = l.market_ticker)
)`, cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Info("cleaned orphan lifecycle", "count", n)
	}

	// Orphan event_lifecycle_events
	res, err = d.db.ExecContext(ctx, `
DELETE FROM event_lifecycle_events WHERE id IN (
    SELECT el.id FROM event_lifecycle_events el
    WHERE el.ts < ?
    AND NOT EXISTS (SELECT 1 FROM events e WHERE e.event_ticker = el.event_ticker)
)`, cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Info("cleaned orphan event lifecycle", "count", n)
	}

	return nil
}

// AdoptOrphans attempts to parent orphan event_lifecycle_events by creating
// the referenced events from their own payload data (series_ticker + title).
// Only fires for rows younger than the threshold — a late-arriving WS message
// might have landed before the REST scanner created the event record.
func (d *DB) AdoptOrphans(ctx context.Context, log *slog.Logger) error {
	cutoff := time.Now().Add(-orphanAgeThreshold).UnixMilli()

	rows, err := d.db.QueryContext(ctx, `
SELECT DISTINCT el.event_ticker, el.series_ticker, el.title, el.subtitle
FROM event_lifecycle_events el
WHERE el.ts > ?
AND NOT EXISTS (SELECT 1 FROM events e WHERE e.event_ticker = el.event_ticker)`, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()

	now := nowMillis()
	adopted := 0
	for rows.Next() {
		var ticker, series, title, subtitle string
		if err := rows.Scan(&ticker, &series, &title, &subtitle); err != nil {
			continue
		}
		if ticker == "" || series == "" {
			continue
		}
		_, err := d.db.ExecContext(ctx, `
INSERT OR IGNORE INTO events (event_ticker, series_ticker, title, sub_title, first_seen_ts, last_updated_ts)
VALUES (?, ?, ?, ?, ?, ?)`,
			ticker, series, title, subtitle, now, now)
		if err != nil {
			log.Warn("adopt orphan failed", "ticker", ticker, "err", err)
			continue
		}
		adopted++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if adopted > 0 {
		log.Info("adopted orphan events", "count", adopted)
	}
	return nil
}
