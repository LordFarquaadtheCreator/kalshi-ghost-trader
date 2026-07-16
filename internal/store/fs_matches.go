package store

import (
	"context"
	"database/sql"
)

// fsMatchSelectColumns is the shared column list for fs match queries.
const fsMatchSelectColumns = `
SELECT fs_match_id, event_ticker, home_player, away_player,
    tournament, surface, category, start_ts, fs_status, last_polled_ts
FROM flashscore_matches`

// scanFSMatch scans a flashscore_matches row.
func scanFSMatch(rows *sql.Rows) (FSMatch, error) {
	var m FSMatch
	var eventTicker, tournament, surface, category sql.NullString
	var startTS, lastPolled sql.NullInt64
	if err := rows.Scan(
		&m.FSMatchID, &eventTicker, &m.HomePlayer, &m.AwayPlayer,
		&tournament, &surface, &category, &startTS, &m.FSStatus, &lastPolled,
	); err != nil {
		return m, err
	}
	m.EventTicker = eventTicker.String
	m.Tournament = tournament.String
	m.Surface = surface.String
	m.Category = category.String
	m.StartTS = startTS.Int64
	m.LastPolledTS = lastPolled.Int64
	return m, nil
}

// UpsertFSMatch inserts or updates a FlashScore match mapping.
func (d *DB) UpsertFSMatch(ctx context.Context, m FSMatch) error {
	now := nowMillis()
	res, err := d.db.ExecContext(ctx, `
INSERT OR IGNORE INTO flashscore_matches (fs_match_id, event_ticker, home_player, away_player,
    tournament, surface, category, start_ts, fs_status, last_polled_ts,
    first_seen_ts, last_updated_ts)
VALUES (?,?,?,?,?,?,?, ?,?, ?, ?,?)`,
		m.FSMatchID, nullableStr(m.EventTicker), m.HomePlayer, m.AwayPlayer,
		nullableStr(m.Tournament), nullableStr(m.Surface), nullableStr(m.Category),
		nullableInt64(m.StartTS), m.FSStatus, nullableInt64(m.LastPolledTS),
		now, now,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil // new
	}
	_, err = d.db.ExecContext(ctx, `
UPDATE flashscore_matches SET event_ticker=?, tournament=?, surface=?, category=?,
    start_ts=?, fs_status=?, last_polled_ts=?, last_updated_ts=?
WHERE fs_match_id=?`,
		nullableStr(m.EventTicker), nullableStr(m.Tournament), nullableStr(m.Surface),
		nullableStr(m.Category), nullableInt64(m.StartTS), m.FSStatus,
		nullableInt64(m.LastPolledTS), now, m.FSMatchID,
	)
	return err
}

// UpdateFSMatchPolled updates last_polled_ts and fs_status after a poll.
func (d *DB) UpdateFSMatchPolled(ctx context.Context, fsMatchID string, fsStatus int) error {
	now := nowMillis()
	_, err := d.db.ExecContext(ctx, `
UPDATE flashscore_matches SET last_polled_ts=?, fs_status=?, last_updated_ts=?
WHERE fs_match_id=?`, now, fsStatus, now, fsMatchID)
	return err
}

// MapFSMatchToEvent links a FlashScore match to a Kalshi event_ticker.
func (d *DB) MapFSMatchToEvent(ctx context.Context, fsMatchID, eventTicker string) error {
	now := nowMillis()
	_, err := d.db.ExecContext(ctx, `
UPDATE flashscore_matches SET event_ticker=?, last_updated_ts=?
WHERE fs_match_id=?`, eventTicker, now, fsMatchID)
	return err
}

// GetFSMatch returns a FlashScore match by ID.
func (d *DB) GetFSMatch(ctx context.Context, fsMatchID string) (FSMatch, error) {
	rows, err := d.db.QueryContext(ctx, fsMatchSelectColumns+` WHERE fs_match_id = ?`, fsMatchID)
	if err != nil {
		return FSMatch{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return FSMatch{}, sql.ErrNoRows
	}
	return scanFSMatch(rows)
}

// GetUnmappedFSMatches returns FlashScore matches not yet linked to Kalshi events.
func (d *DB) GetUnmappedFSMatches(ctx context.Context) ([]FSMatch, error) {
	rows, err := d.db.QueryContext(ctx,
		fsMatchSelectColumns+` WHERE event_ticker IS NULL ORDER BY start_ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []FSMatch
	for rows.Next() {
		m, err := scanFSMatch(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// GetFSMatchesByEvent returns all FlashScore matches mapped to a Kalshi event.
func (d *DB) GetFSMatchesByEvent(ctx context.Context, eventTicker string) ([]FSMatch, error) {
	rows, err := d.db.QueryContext(ctx,
		fsMatchSelectColumns+` WHERE event_ticker = ?`, eventTicker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []FSMatch
	for rows.Next() {
		m, err := scanFSMatch(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt64(n int64) interface{} {
	if n == 0 {
		return nil
	}
	return n
}
