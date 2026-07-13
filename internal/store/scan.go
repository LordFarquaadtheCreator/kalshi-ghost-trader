package store

import "context"

// RecordScanRun logs a scan run.
func (d *DB) RecordScanRun(ctx context.Context, seriesTicker string, eventsFound, marketsFound, newEvents, newMarkets int) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO scan_runs (run_ts, series_ticker, events_found, markets_found, new_events, new_markets)
VALUES (?,?,?,?,?,?)`,
		nowMillis(), seriesTicker, eventsFound, marketsFound, newEvents, newMarkets,
	)
	return err
}
