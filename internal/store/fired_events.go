package store

import (
	"context"

	"gorm.io/gorm/clause"
)

// MarkFired records that a strategy has fired on an event.
// Idempotent — INSERT OR IGNORE.
func (d *DB) MarkFired(ctx context.Context, eventTicker, strategy string) error {
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&FiredEvent{
		EventTicker: eventTicker,
		Strategy:    strategy,
		FiredTS:     nowMillis(),
	}).Error
}

// IsFired checks if a strategy has already fired on an event.
func (d *DB) IsFired(ctx context.Context, eventTicker, strategy string) (bool, error) {
	var count int64
	err := d.db.WithContext(ctx).Model(&FiredEvent{}).
		Where("event_ticker = ? AND strategy = ?", eventTicker, strategy).Count(&count).Error
	return count > 0, err
}

// LoadFiredEvents returns all event_tickers a strategy has fired on.
func (d *DB) LoadFiredEvents(ctx context.Context, strategy string) (map[string]bool, error) {
	var fired []FiredEvent
	err := d.db.WithContext(ctx).Select("event_ticker").Where("strategy = ?", strategy).Find(&fired).Error
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(fired))
	for _, f := range fired {
		m[f.EventTicker] = true
	}
	return m, nil
}

// ClearFired removes a fired event record (e.g. on UnregisterMarkets).
func (d *DB) ClearFired(ctx context.Context, eventTicker, strategy string) error {
	return d.db.WithContext(ctx).Where("event_ticker = ? AND strategy = ?", eventTicker, strategy).
		Delete(&FiredEvent{}).Error
}
