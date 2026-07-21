package store

import "context"

// InsertOrderbookBatch inserts a batch of orderbook events in one transaction.
func (d *DB) InsertOrderbookBatch(ctx context.Context, events []OrderbookEvent) error {
	if len(events) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).CreateInBatches(&events, len(events)).Error
}
