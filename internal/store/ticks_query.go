package store

import (
	"context"

	"gorm.io/gorm"
)

// GetTicksByMarket returns all ticks for a market, ordered by ts.
// Used by replay tests to reconstruct historical price data.
func (d *DB) GetTicksByMarket(ctx context.Context, marketTicker string) ([]Tick, error) {
	var ticks []Tick
	err := d.db.WithContext(ctx).Where("market_ticker = ?", marketTicker).Order("ts").Find(&ticks).Error
	return ticks, err
}

// GetLatestDollarVolume returns the most recent dollar_volume for a market.
// Used by volratio strategy in live mode.
func (d *DB) GetLatestDollarVolume(ctx context.Context, marketTicker string) (float64, error) {
	var t Tick
	err := d.db.WithContext(ctx).Select("dollar_volume").
		Where("market_ticker = ? AND dollar_volume IS NOT NULL AND dollar_volume > 0", marketTicker).
		Order("ts DESC").First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	return float64(t.DollarVolume), err
}
