package store

import (
	"time"

	"gorm.io/gorm/clause"
)

// SaveBacktestResult upserts a single strategy's backtest result.
func (db *DB) SaveBacktestResult(row BacktestResultRow) error {
	row.UpdatedAt = time.Now().UnixMilli()
	return db.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "strategy"}},
		DoUpdates: clause.AssignmentColumns([]string{"run_ts", "match_count", "summary_json", "orders_json", "cum_pnl_json", "updated_at"}),
	}).Create(&row).Error
}

// GetAllBacktestResults returns all persisted backtest result rows.
func (db *DB) GetAllBacktestResults() ([]BacktestResultRow, error) {
	var rows []BacktestResultRow
	err := db.db.Find(&rows).Error
	return rows, err
}

// GetBacktestRunTS returns the latest run_ts across all strategies.
// Returns 0 if no results exist yet.
func (db *DB) GetBacktestRunTS() (int64, error) {
	var maxTS int64
	err := db.db.Raw(`SELECT COALESCE(MAX(run_ts), 0) FROM backtest_results`).Scan(&maxTS).Error
	return maxTS, err
}

// GetLastFinalizedSettlementTS returns the max settlement_ts among finalized markets.
// Used to detect if new data arrived since last backtest compute.
func (db *DB) GetLastFinalizedSettlementTS() (int64, error) {
	var maxTS int64
	err := db.db.Raw(`SELECT COALESCE(MAX(settlement_ts), 0) FROM markets WHERE status = 'finalized'`).Scan(&maxTS).Error
	return maxTS, err
}
