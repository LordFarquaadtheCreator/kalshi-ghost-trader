package store

import (
	"context"
	"fmt"

	"gorm.io/gorm/clause"
)

// GetPointsByMatch returns all point-by-point entries for a match, ordered by time.
func (d *DB) GetPointsByMatch(ctx context.Context, matchTicker string) ([]Point, error) {
	var points []Point
	err := d.db.WithContext(ctx).Where("match_ticker = ?", matchTicker).
		Order("ts_ms ASC, set_number ASC, game_number ASC, point_number ASC").
		Find(&points).Error
	if err != nil {
		return nil, fmt.Errorf("get points: %w", err)
	}
	return points, nil
}

// GetMatchTickersWithPoints returns event tickers that have point data.
func (d *DB) GetMatchTickersWithPoints(ctx context.Context) ([]string, error) {
	var tickers []string
	err := d.db.WithContext(ctx).Model(&Point{}).
		Where("ts_ms IS NOT NULL").Distinct("match_ticker").
		Pluck("match_ticker", &tickers).Error
	if err != nil {
		return nil, fmt.Errorf("get match tickers with points: %w", err)
	}
	return tickers, nil
}

// InsertPointBatch inserts a batch of point entries. Uses INSERT OR IGNORE
// to deduplicate on (match_ticker, set_number, game_number, point_number).
func (d *DB) InsertPointBatch(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(&points, len(points)).Error
}

// UpdatePointFlags sets is_set_point and is_match_point for existing rows.
// Used by backfill to fix historical data that was inserted without flags.
func (d *DB) UpdatePointFlags(ctx context.Context, matchTicker string, setNumber, gameNumber, pointNumber int, isSetPoint, isMatchPoint bool) error {
	return d.db.WithContext(ctx).Model(&Point{}).
		Where("match_ticker = ? AND set_number = ? AND game_number = ? AND point_number = ?",
			matchTicker, setNumber, gameNumber, pointNumber).
		Updates(map[string]any{
			"is_set_point":    isSetPoint,
			"is_match_point": isMatchPoint,
		}).Error
}

// GetAllPoints returns all points ordered by match and time. Used for backfill.
func (d *DB) GetAllPoints(ctx context.Context) ([]Point, error) {
	var points []Point
	err := d.db.WithContext(ctx).
		Order("match_ticker, set_number, game_number, point_number").
		Find(&points).Error
	if err != nil {
		return nil, fmt.Errorf("get all points: %w", err)
	}
	return points, nil
}
