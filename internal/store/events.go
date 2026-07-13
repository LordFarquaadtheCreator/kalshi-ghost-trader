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

// GetAllEventsForMatching returns all events for FlashScore name matching.
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
