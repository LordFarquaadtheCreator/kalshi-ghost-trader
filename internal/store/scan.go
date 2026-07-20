package store

import "context"

// RecordScanRun logs a scan run.
func (d *DB) RecordScanRun(ctx context.Context, seriesTicker string, eventsFound, marketsFound, newEvents, newMarkets int) error {
	return d.db.WithContext(ctx).Create(&ScanRun{
		RunTS:        nowMillis(),
		SeriesTicker: seriesTicker,
		EventsFound:  eventsFound,
		MarketsFound: marketsFound,
		NewEvents:    newEvents,
		NewMarkets:   newMarkets,
	}).Error
}
