package store

import "context"

// MarkFired records that a strategy has fired on an event.
// Idempotent — INSERT OR IGNORE.
func (d *DB) MarkFired(ctx context.Context, eventTicker, strategy string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO fired_events (event_ticker, strategy, fired_ts) VALUES (?, ?, ?)`,
		eventTicker, strategy, nowMillis())
	return err
}

// IsFired checks if a strategy has already fired on an event.
func (d *DB) IsFired(ctx context.Context, eventTicker, strategy string) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fired_events WHERE event_ticker = ? AND strategy = ?`,
		eventTicker, strategy).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// LoadFiredEvents returns all event_tickers a strategy has fired on.
func (d *DB) LoadFiredEvents(ctx context.Context, strategy string) (map[string]bool, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT event_ticker FROM fired_events WHERE strategy = ?`, strategy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]bool)
	for rows.Next() {
		var et string
		if err := rows.Scan(&et); err != nil {
			return nil, err
		}
		m[et] = true
	}
	return m, nil
}

// ClearFired removes a fired event record (e.g. on UnregisterMarkets).
func (d *DB) ClearFired(ctx context.Context, eventTicker, strategy string) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM fired_events WHERE event_ticker = ? AND strategy = ?`,
		eventTicker, strategy)
	return err
}
