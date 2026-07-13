package store

import "context"

// UpsertEvent inserts or updates an event row.
func (d *DB) UpsertEvent(ctx context.Context, e Event) error {
	_, err := d.UpsertEventCheckNew(ctx, e)
	return err
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
