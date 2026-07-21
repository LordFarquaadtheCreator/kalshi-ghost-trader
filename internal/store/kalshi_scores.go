package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// UpsertKalshiScore inserts or replaces a Kalshi live score snapshot for an event.
// Called by the kalshilivedata poller on every poll cycle.
func (d *DB) UpsertKalshiScore(ctx context.Context, s KalshiScore) error {
	return d.db.WithContext(ctx).Save(&s).Error
}

// GetKalshiScores returns live score snapshots for the given event tickers.
// Used by Engine.LatestScores to fill gaps where API-Tennis has no data.
func (d *DB) GetKalshiScores(ctx context.Context, eventTickers []string) (map[string]KalshiScore, error) {
	if len(eventTickers) == 0 {
		return map[string]KalshiScore{}, nil
	}
	var rows []KalshiScore
	err := d.db.WithContext(ctx).Where("event_ticker IN ?", eventTickers).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("get kalshi_scores: %w", err)
	}
	out := make(map[string]KalshiScore, len(rows))
	for _, r := range rows {
		out[r.EventTicker] = r
	}
	return out, nil
}

// HasAPItennisPoints returns true if the points table has any entries
// for the given event_ticker from the API-Tennis scraper (fs_match_id
// is not prefixed with "kalshi-").
func (d *DB) HasAPItennisPoints(ctx context.Context, eventTicker string) (bool, error) {
	var count int64
	err := d.db.WithContext(ctx).
		Table("points").
		Where("match_ticker = ? AND fs_match_id NOT LIKE 'kalshi-%'", eventTicker).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check apitennis points: %w", err)
	}
	return count > 0, nil
}

// HasPoints returns true if the points table has any entries for the given
// event_ticker, regardless of source (API-Tennis or Kalshi live_data).
// Used by the real order emitter to bypass the scheduled occurrence_ts gate
// when points have already been recorded — match started ahead of schedule.
func (d *DB) HasPoints(ctx context.Context, eventTicker string) (bool, error) {
	var count int64
	err := d.db.WithContext(ctx).
		Table("points").
		Where("match_ticker = ?", eventTicker).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check points: %w", err)
	}
	return count > 0, nil
}

// GetKalshiScore returns the live score snapshot for a single event.
// Returns gorm.ErrRecordNotFound if no snapshot exists.
func (d *DB) GetKalshiScore(ctx context.Context, eventTicker string) (KalshiScore, error) {
	var s KalshiScore
	err := d.db.WithContext(ctx).Where("event_ticker = ?", eventTicker).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s, err
		}
		return s, fmt.Errorf("get kalshi_score: %w", err)
	}
	return s, nil
}
