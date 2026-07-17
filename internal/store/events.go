package store

import (
	"context"
	"database/sql"
)

// UpsertEvent inserts or updates an event row.
func (d *DB) UpsertEvent(ctx context.Context, e Event) error {
	_, err := d.UpsertEventCheckNew(ctx, e)
	return err
}

// DeleteEvent removes an event by ticker. Cascade triggers handle child rows.
func (d *DB) DeleteEvent(ctx context.Context, eventTicker string) (int64, error) {
	res, err := d.db.ExecContext(ctx, "DELETE FROM events WHERE event_ticker = ?", eventTicker)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EventExists returns true if the event is already in the DB.
func (d *DB) EventExists(ctx context.Context, eventTicker string) (bool, error) {
	var exists int
	err := d.db.QueryRowContext(ctx, "SELECT 1 FROM events WHERE event_ticker = ? LIMIT 1", eventTicker).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// GetSeriesTicker returns the series_ticker for an event.
func (d *DB) GetSeriesTicker(ctx context.Context, eventTicker string) (string, error) {
	var series string
	err := d.db.QueryRowContext(ctx, "SELECT series_ticker FROM events WHERE event_ticker = ?", eventTicker).Scan(&series)
	return series, err
}

// SetCoverage computes and stores the coverage tag for an event's markets.
// Called after settlement in ApplyLifecycleEvent. Classification:
//
//	full     — winner market has >=100 ticks spanning >=290s in the final 5-min window
//	low_freq — winner market has 1-99 ticks in that window
//	none     — no ticks on either market
func (d *DB) SetCoverage(ctx context.Context, eventTicker string) error {
	_, err := d.db.ExecContext(ctx, `
UPDATE events SET coverage = (
    SELECT CASE
        WHEN COALESCE((SELECT COUNT(*) FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker
             WHERE m.event_ticker = ?1 AND m.result = 'yes'
             AND t.ts >= (SELECT close_ts - 300000 FROM markets WHERE event_ticker = ?1 AND result = 'yes' LIMIT 1)), 0) >= 100
         AND COALESCE((SELECT COALESCE(MAX(ts)-MIN(ts), 0) FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker
             WHERE m.event_ticker = ?1 AND m.result = 'yes'
             AND t.ts >= (SELECT close_ts - 300000 FROM markets WHERE event_ticker = ?1 AND result = 'yes' LIMIT 1)), 0) >= 290000
            THEN 'full'
        WHEN EXISTS (SELECT 1 FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker WHERE m.event_ticker = ?1)
            THEN 'low_freq'
        ELSE 'none'
    END
) WHERE event_ticker = ?1`, eventTicker)
	return err
}

// DropOrphanPayloads nulls the raw payload column for settled events whose
// DropOrphanPayloads nulls the raw payload column for settled events whose
// coverage is not 'full'. Saves significant disk space — payloads dominate.
func (d *DB) DropOrphanPayloads(ctx context.Context, eventTicker string) error {
	_, _ = d.db.ExecContext(ctx, `
UPDATE ticks SET payload = NULL
WHERE market_ticker IN (SELECT market_ticker FROM markets WHERE event_ticker = ?)
  AND id IN (SELECT t.id FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker WHERE m.event_ticker = ?)
  AND (SELECT coverage FROM events WHERE event_ticker = ?) != 'full'`,
		eventTicker, eventTicker, eventTicker)
	_, _ = d.db.ExecContext(ctx, `
UPDATE orderbook_events SET payload = NULL
WHERE market_ticker IN (SELECT market_ticker FROM markets WHERE event_ticker = ?)
  AND (SELECT coverage FROM events WHERE event_ticker = ?) != 'full'`,
		eventTicker, eventTicker)
	return nil
}

// GetCoverage returns the stored coverage tag for an event.
func (d *DB) GetCoverage(ctx context.Context, eventTicker string) (string, error) {
	var cov sql.NullString
	err := d.db.QueryRowContext(ctx, "SELECT coverage FROM events WHERE event_ticker = ?", eventTicker).Scan(&cov)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return cov.String, nil
}

// FinalizeEventIfNeeded runs post-settlement logic for an event if both
// markets are finalized. Extracted from ApplyLifecycleEvent "settled" case
// so the reconciler can trigger the same cleanup via REST.
//
// Skips pruning for events that have orders — orders are valuable even with
// no tick/point data (P6 protection).
func (d *DB) FinalizeEventIfNeeded(ctx context.Context, eventTicker string) error {
	var pending int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM markets WHERE event_ticker = ? AND status != 'finalized'", eventTicker).Scan(&pending)
	if err != nil || pending > 0 {
		return err
	}

	_ = d.SetCoverage(ctx, eventTicker)

	cov, _ := d.GetCoverage(ctx, eventTicker)
	if cov == "none" {
		// Check for orders before pruning — don't delete events with order data
		var orderCount int
		d.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM orders WHERE match_ticker = ?", eventTicker).Scan(&orderCount)
		if orderCount == 0 {
			_, _ = d.db.ExecContext(ctx, "DELETE FROM events WHERE event_ticker = ?", eventTicker)
		}
		return nil
	}

	if cov != "full" && cov != "" {
		_ = d.DropOrphanPayloads(ctx, eventTicker)
	}
	return nil
}

// GetAllEventsForMatching returns all events for API-Tennis name matching.
// Ordered by last_updated descending so recent events are matched first.
func (d *DB) GetAllEventsForMatching(ctx context.Context) ([]Event, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT event_ticker, series_ticker, title, sub_title, competition, competition_scope,
    mutually_exclusive
FROM events ORDER BY last_updated_ts DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var competition, competitionScope sql.NullString
		if err := rows.Scan(
			&e.EventTicker, &e.SeriesTicker, &e.Title, &e.SubTitle,
			&competition, &competitionScope, &e.MutuallyExclusive,
		); err != nil {
			return nil, err
		}
		e.Competition = competition.String
		e.CompetitionScope = competitionScope.String
		events = append(events, e)
	}
	return events, rows.Err()
}

// UpsertEventCheckNew inserts or updates an event. Returns true if new.
func (d *DB) UpsertEventCheckNew(ctx context.Context, e Event) (bool, error) {
	now := nowMillis()
	// INSERT OR IGNORE — rows affected = 1 if new, 0 if existed
	res, err := d.db.ExecContext(ctx, `
INSERT OR IGNORE INTO events (event_ticker, series_ticker, title, sub_title, competition, competition_scope,
    mutually_exclusive, first_seen_ts, last_updated_ts)
VALUES (?,?,?,?,?,?,?, ?,?)`,
		e.EventTicker, e.SeriesTicker, e.Title, e.SubTitle,
		e.Competition, e.CompetitionScope, e.MutuallyExclusive,
		now, now,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return true, nil // new
	}
	// Existed — update
	_, err = d.db.ExecContext(ctx, `
UPDATE events SET title=?, sub_title=?, competition=?, competition_scope=?,
    mutually_exclusive=?, last_updated_ts=?
WHERE event_ticker=?`,
		e.Title, e.SubTitle, e.Competition, e.CompetitionScope,
		e.MutuallyExclusive, now, e.EventTicker,
	)
	return false, err
}
