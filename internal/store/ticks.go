package store

import "context"

// InsertTickBatch inserts a batch of ticks in one transaction.
func (d *DB) InsertTickBatch(ctx context.Context, ticks []Tick) error {
	if len(ticks) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).CreateInBatches(&ticks, len(ticks)).Error
}
