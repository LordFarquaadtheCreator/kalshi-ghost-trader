package store

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm/clause"
)

const orphanAgeThreshold = 6 * time.Hour

// CleanOrphans removes child rows that reference parents that don't exist
// and are older than the age threshold (to avoid deleting rows whose parent
// might still be in-flight from a race condition).
func (d *DB) CleanOrphans(ctx context.Context, log *slog.Logger) error {
	cutoff := time.Now().Add(-orphanAgeThreshold).UnixMilli()

	// Orphan ticks: no parent market, and old enough
	res := d.db.WithContext(ctx).Exec(`
DELETE FROM ticks WHERE id IN (
    SELECT t.id FROM ticks t
    WHERE t.ts < ?
    AND NOT EXISTS (SELECT 1 FROM markets m WHERE m.market_ticker = t.market_ticker)
)`, cutoff)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Info("cleaned orphan ticks", "count", res.RowsAffected)
	}

	// Orphan orderbook_events
	res = d.db.WithContext(ctx).Exec(`
DELETE FROM orderbook_events WHERE id IN (
    SELECT o.id FROM orderbook_events o
    WHERE o.ts < ?
    AND NOT EXISTS (SELECT 1 FROM markets m WHERE m.market_ticker = o.market_ticker)
)`, cutoff)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Info("cleaned orphan orderbook", "count", res.RowsAffected)
	}

	// Orphan lifecycle_events
	res = d.db.WithContext(ctx).Exec(`
DELETE FROM lifecycle_events WHERE id IN (
    SELECT l.id FROM lifecycle_events l
    WHERE l.ts < ?
    AND NOT EXISTS (SELECT 1 FROM markets m WHERE m.market_ticker = l.market_ticker)
)`, cutoff)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Info("cleaned orphan lifecycle", "count", res.RowsAffected)
	}

	// Orphan event_lifecycle_events
	res = d.db.WithContext(ctx).Exec(`
DELETE FROM event_lifecycle_events WHERE id IN (
    SELECT el.id FROM event_lifecycle_events el
    WHERE el.ts < ?
    AND NOT EXISTS (SELECT 1 FROM events e WHERE e.event_ticker = el.event_ticker)
)`, cutoff)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Info("cleaned orphan event lifecycle", "count", res.RowsAffected)
	}

	return nil
}

// AdoptOrphans attempts to parent orphan event_lifecycle_events by creating
// the referenced events from their own payload data (series_ticker + title).
// Only fires for rows younger than the threshold — a late-arriving WS message
// might have landed before the REST scanner created the event record.
func (d *DB) AdoptOrphans(ctx context.Context, log *slog.Logger) error {
	cutoff := time.Now().Add(-orphanAgeThreshold).UnixMilli()

	var orphans []EventLifecycleEvent
	err := d.db.WithContext(ctx).Raw(`
SELECT DISTINCT el.event_ticker, el.series_ticker, el.title, el.subtitle
FROM event_lifecycle_events el
WHERE el.ts > ?
AND NOT EXISTS (SELECT 1 FROM events e WHERE e.event_ticker = el.event_ticker)`, cutoff).
		Scan(&orphans).Error
	if err != nil {
		return err
	}

	now := nowMillis()
	adopted := 0
	for _, el := range orphans {
		if el.EventTicker == "" || el.SeriesTicker == "" {
			continue
		}
		err := d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&Event{
			EventTicker:   el.EventTicker,
			SeriesTicker:  el.SeriesTicker,
			Title:         el.Title,
			SubTitle:      el.Subtitle,
			FirstSeenTS:   now,
			LastUpdatedTS: now,
		}).Error
		if err != nil {
			log.Warn("adopt orphan failed", "ticker", el.EventTicker, "err", err)
			continue
		}
		adopted++
	}
	if adopted > 0 {
		log.Info("adopted orphan events", "count", adopted)
	}
	return nil
}
