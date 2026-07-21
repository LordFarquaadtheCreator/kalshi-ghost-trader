package store

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertEvent inserts or updates an event row.
func (d *DB) UpsertEvent(ctx context.Context, e Event) error {
	_, err := d.UpsertEventCheckNew(ctx, e)
	return err
}

// DeleteEvent removes an event by ticker. Cascade triggers handle child rows.
func (d *DB) DeleteEvent(ctx context.Context, eventTicker string) (int64, error) {
	res := d.db.WithContext(ctx).Where("event_ticker = ?", eventTicker).Delete(&Event{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// EventExists returns true if the event is already in the DB.
func (d *DB) EventExists(ctx context.Context, eventTicker string) (bool, error) {
	var count int64
	err := d.db.WithContext(ctx).Model(&Event{}).Where("event_ticker = ?", eventTicker).Count(&count).Error
	return count > 0, err
}

// GetSeriesTicker returns the series_ticker for an event.
func (d *DB) GetSeriesTicker(ctx context.Context, eventTicker string) (string, error) {
	var e Event
	err := d.db.WithContext(ctx).Select("series_ticker").Where("event_ticker = ?", eventTicker).First(&e).Error
	return e.SeriesTicker, err
}

// GetEventTitle returns the title for an event by ticker.
func (d *DB) GetEventTitle(ctx context.Context, eventTicker string) (string, error) {
	var e Event
	err := d.db.WithContext(ctx).Select("title").Where("event_ticker = ?", eventTicker).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return e.Title, err
}

// GetSurface returns the court surface for an event by joining flashscore_matches.
// Returns empty string if no flashscore match is mapped or surface is null.
func (d *DB) GetSurface(ctx context.Context, eventTicker string) (string, error) {
	var fm FlashscoreMatch
	err := d.db.WithContext(ctx).Where("event_ticker = ? AND surface IS NOT NULL AND surface != ''", eventTicker).First(&fm).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return fm.Surface, err
}

// SetCoverage computes and stores the coverage tag for an event's markets.
// Called after settlement in ApplyLifecycleEvent. Classification:
//
//	full     — winner market has >=100 ticks spanning >=290s in the final 5-min window
//	low_freq — winner market has 1-99 ticks in that window
//	none     — no ticks on either market
func (d *DB) SetCoverage(ctx context.Context, eventTicker string) error {
	return d.db.WithContext(ctx).Exec(`
UPDATE events SET coverage = (
    SELECT CASE
        WHEN COALESCE((SELECT COUNT(*) FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker
             WHERE m.event_ticker = $1 AND m.result = 'yes'
             AND t.ts >= (SELECT close_ts - 300000 FROM markets WHERE event_ticker = $1 AND result = 'yes' LIMIT 1)), 0) >= 100
         AND COALESCE((SELECT COALESCE(MAX(ts)-MIN(ts), 0) FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker
             WHERE m.event_ticker = $1 AND m.result = 'yes'
             AND t.ts >= (SELECT close_ts - 300000 FROM markets WHERE event_ticker = $1 AND result = 'yes' LIMIT 1)), 0) >= 290000
            THEN 'full'
        WHEN EXISTS (SELECT 1 FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker WHERE m.event_ticker = $1)
            THEN 'low_freq'
        ELSE 'none'
    END
) WHERE event_ticker = $1`, eventTicker).Error
}

// DropOrphanPayloads nulls the raw payload column for settled events whose
// coverage is not 'full'. Saves significant disk space — payloads dominate.
func (d *DB) DropOrphanPayloads(ctx context.Context, eventTicker string) error {
	d.db.WithContext(ctx).Exec(`
UPDATE ticks SET payload = NULL
WHERE market_ticker IN (SELECT market_ticker FROM markets WHERE event_ticker = ?)
  AND id IN (SELECT t.id FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker WHERE m.event_ticker = ?)
  AND (SELECT coverage FROM events WHERE event_ticker = ?) != 'full'`,
		eventTicker, eventTicker, eventTicker)
	d.db.WithContext(ctx).Exec(`
UPDATE orderbook_events SET payload = NULL
WHERE market_ticker IN (SELECT market_ticker FROM markets WHERE event_ticker = ?)
  AND (SELECT coverage FROM events WHERE event_ticker = ?) != 'full'`,
		eventTicker, eventTicker)
	return nil
}

// GetCoverage returns the stored coverage tag for an event.
func (d *DB) GetCoverage(ctx context.Context, eventTicker string) (string, error) {
	var e Event
	err := d.db.WithContext(ctx).Select("coverage").Where("event_ticker = ?", eventTicker).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return e.Coverage, err
}

// FinalizeEventIfNeeded runs post-settlement logic for an event if both
// markets are finalized. Extracted from ApplyLifecycleEvent "settled" case
// so the reconciler can trigger the same cleanup via REST.
//
// Skips pruning for events that have orders — orders are valuable even with
// no tick/point data (P6 protection).
func (d *DB) FinalizeEventIfNeeded(ctx context.Context, eventTicker string) error {
	var pending int64
	err := d.db.WithContext(ctx).Model(&Market{}).Where("event_ticker = ? AND status != 'finalized'", eventTicker).Count(&pending).Error
	if err != nil || pending > 0 {
		return err
	}

	_ = d.SetCoverage(ctx, eventTicker)

	cov, _ := d.GetCoverage(ctx, eventTicker)
	if cov == "none" {
		var orderCount int64
		d.db.WithContext(ctx).Model(&Order{}).Where("match_ticker = ?", eventTicker).Count(&orderCount)
		if orderCount == 0 {
			d.db.WithContext(ctx).Where("event_ticker = ?", eventTicker).Delete(&Event{})
		}
		return nil
	}

	if cov != "full" && cov != "" {
		_ = d.DropOrphanPayloads(ctx, eventTicker)
	}
	return nil
}

// GetAllEventsForMatching returns all events for API-Tennis name matching.
// Ordered by last_updated descending so recent events are matched first.
func (d *DB) GetAllEventsForMatching(ctx context.Context) ([]Event, error) {
	var events []Event
	err := d.db.WithContext(ctx).Select("event_ticker, series_ticker, title, sub_title, competition, competition_scope, mutually_exclusive").
		Order("last_updated_ts DESC").Find(&events).Error
	return events, err
}

// UpsertEventCheckNew inserts or updates an event. Returns true if new.
func (d *DB) UpsertEventCheckNew(ctx context.Context, e Event) (bool, error) {
	now := nowMillis()
	e.FirstSeenTS = now
	e.LastUpdatedTS = now

	res := d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&e)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}

	// Existed — update
	res = d.db.WithContext(ctx).Model(&Event{}).Where("event_ticker = ?", e.EventTicker).
		Updates(map[string]any{
			"title":              e.Title,
			"sub_title":          e.SubTitle,
			"competition":        e.Competition,
			"competition_scope":  e.CompetitionScope,
			"mutually_exclusive": e.MutuallyExclusive,
			"last_updated_ts":    now,
		})
	return false, res.Error
}
