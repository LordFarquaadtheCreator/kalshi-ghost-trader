package store

import "gorm.io/gorm"

// Price band result persistence.
//
// The price_band_results table stores per-day, per-strategy, per-band
// aggregates computed by the cron goroutine in internal/pricebands.
// The cron only computes days not already in the table — diff between
// source days (from finalized markets) and computed days.

// GetComputedDays returns the set of days already present in price_band_results.
func (d *DB) GetComputedDays() ([]string, error) {
	var days []string
	if err := d.db.Raw(`SELECT DISTINCT day FROM price_band_results ORDER BY day`).Scan(&days).Error; err != nil {
		return nil, err
	}
	return days, nil
}

// SavePriceBandDay replaces all rows for a given day with fresh computations.
// Single transaction: delete old rows, insert new ones.
func (d *DB) SavePriceBandDay(runTS int64, day string, rows []PriceBandResultRow) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("day = ?", day).Delete(&PriceBandResultRow{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for i := range rows {
			rows[i].RunTS = runTS
			rows[i].Day = day
		}
		return tx.Create(&rows).Error
	})
}

// GetAllPriceBandResults returns all rows from price_band_results.
// Dashboard reads everything and groups/sorts client-side.
func (d *DB) GetAllPriceBandResults() ([]PriceBandResultRow, error) {
	var rows []PriceBandResultRow
	if err := d.db.Order("day, strategy, band_label").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetPriceBandRunTS returns the most recent run_ts across all rows.
func (d *DB) GetPriceBandRunTS() (int64, error) {
	var ts int64
	if err := d.db.Raw(`SELECT COALESCE(MAX(run_ts), 0) FROM price_band_results`).Scan(&ts).Error; err != nil {
		return 0, err
	}
	return ts, nil
}
