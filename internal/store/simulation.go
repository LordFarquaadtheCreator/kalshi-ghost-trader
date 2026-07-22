package store

import "gorm.io/gorm"

// Simulation insights persistence.
//
// simulation_insights stores per-strategy × per-day × per-fixed-band
// aggregates with derived metrics (sharpe, profit_factor, max_drawdown,
// score, peak flag). Computed by the pricebands cron alongside
// price_band_results. Read by /api/simulation endpoint.

// GetComputedInsightDays returns distinct days already in simulation_insights.
func (d *DB) GetComputedInsightDays() ([]string, error) {
	var days []string
	if err := d.db.Raw(`SELECT DISTINCT day FROM simulation_insights ORDER BY day`).Scan(&days).Error; err != nil {
		return nil, err
	}
	return days, nil
}

// SaveSimulationInsightDay replaces all rows for a given day, inserts new ones.
// Single transaction: delete old, insert new.
func (d *DB) SaveSimulationInsightDay(runTS int64, day string, rows []SimulationInsightRow) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("day = ?", day).Delete(&SimulationInsightRow{}).Error; err != nil {
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

// GetAllSimulationInsights returns all rows from simulation_insights.
func (d *DB) GetAllSimulationInsights() ([]SimulationInsightRow, error) {
	var rows []SimulationInsightRow
	if err := d.db.Order("strategy, day, band_lo").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetSimulationInsightRunTS returns the most recent run_ts across all rows.
func (d *DB) GetSimulationInsightRunTS() (int64, error) {
	var ts int64
	if err := d.db.Raw(`SELECT COALESCE(MAX(run_ts), 0) FROM simulation_insights`).Scan(&ts).Error; err != nil {
		return 0, err
	}
	return ts, nil
}
