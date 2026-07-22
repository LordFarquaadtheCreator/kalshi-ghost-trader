package store

import "gorm.io/gorm"

// Paper order insights persistence.
//
// paper_order_insights stores per-strategy × per-day × per-fixed-band
// aggregates with derived metrics, computed from live orders table
// (is_real = false, market resolved). Populated by the paperorderinsights
// cron. Read by /api/paper-orders-insights endpoint.
//
// paper_order_summaries stores per-strategy aggregate + cumulative P&L
// series. Recomputed every cron run (small table, captures new orders
// without waiting for day rollover).

// GetComputedPaperOrderInsightDays returns distinct days already in paper_order_insights.
func (d *DB) GetComputedPaperOrderInsightDays() ([]string, error) {
	var days []string
	if err := d.db.Raw(`SELECT DISTINCT day FROM paper_order_insights ORDER BY day`).Scan(&days).Error; err != nil {
		return nil, err
	}
	return days, nil
}

// SavePaperOrderInsightDay replaces all rows for a given day, inserts new ones.
// Single transaction: delete old, insert new.
func (d *DB) SavePaperOrderInsightDay(runTS int64, day string, rows []PaperOrderInsightRow) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("day = ?", day).Delete(&PaperOrderInsightRow{}).Error; err != nil {
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

// GetAllPaperOrderInsights returns all rows from paper_order_insights.
// Dashboard reads everything and groups/sorts client-side.
func (d *DB) GetAllPaperOrderInsights() ([]PaperOrderInsightRow, error) {
	var rows []PaperOrderInsightRow
	if err := d.db.Order("strategy, day, band_lo").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetPaperOrderInsightRunTS returns the most recent run_ts across all rows.
func (d *DB) GetPaperOrderInsightRunTS() (int64, error) {
	var ts int64
	if err := d.db.Raw(`SELECT COALESCE(MAX(run_ts), 0) FROM paper_order_insights`).Scan(&ts).Error; err != nil {
		return 0, err
	}
	return ts, nil
}

// ReplacePaperOrderSummaries replaces all rows in paper_order_summaries.
// Single transaction: truncate + insert. Called every cron run.
func (d *DB) ReplacePaperOrderSummaries(rows []PaperOrderSummaryRow) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&PaperOrderSummaryRow{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

// GetAllPaperOrderSummaries returns all rows from paper_order_summaries.
func (d *DB) GetAllPaperOrderSummaries() ([]PaperOrderSummaryRow, error) {
	var rows []PaperOrderSummaryRow
	if err := d.db.Order("strategy").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
