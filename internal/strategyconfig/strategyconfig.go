package strategyconfig

import (
	"context"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetAll returns all strategy config entries.
func GetAll(ctx context.Context, db *gorm.DB) ([]store.StrategyConfigEntry, error) {
	var entries []store.StrategyConfigEntry
	err := db.WithContext(ctx).Find(&entries).Error
	return entries, err
}

// SetEnabled enables/disables a strategy for real trading.
// Inserts the row if it doesn't exist.
func SetEnabled(ctx context.Context, db *gorm.DB, strategy string, enabled bool) error {
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "strategy"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_ts"}),
	}).Create(&store.StrategyConfigEntry{
		Strategy:  strategy,
		Enabled:   enabled,
		UpdatedTS: time.Now().UnixMilli(),
	}).Error
}

// IsEnabled returns whether a strategy is enabled for real trading.
// Returns false if the strategy has no config row (default disabled).
func IsEnabled(ctx context.Context, db *gorm.DB, strategy string) (bool, error) {
	var e store.StrategyConfigEntry
	err := db.WithContext(ctx).Where("strategy = ?", strategy).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return e.Enabled, nil
}

// Ensure inserts a strategy_config row if it doesn't exist (disabled by default).
func Ensure(ctx context.Context, db *gorm.DB, strategy string) error {
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&store.StrategyConfigEntry{
		Strategy:  strategy,
		Enabled:   false,
		UpdatedTS: time.Now().UnixMilli(),
	}).Error
}

// GetLimit returns the per-market max orders for a strategy.
// Returns 1 (default) if the strategy has no config row.
func GetLimit(ctx context.Context, db *gorm.DB, strategy string) (int, error) {
	var e store.StrategyConfigEntry
	err := db.WithContext(ctx).Select("per_market_max_orders").
		Where("strategy = ?", strategy).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return e.PerMarketMaxOrders, nil
}

// SetLimit sets the per-market max orders for a strategy.
// Inserts the row if it doesn't exist; preserves enabled flag on update.
func SetLimit(ctx context.Context, db *gorm.DB, strategy string, maxOrders int) error {
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "strategy"}},
		DoUpdates: clause.AssignmentColumns([]string{"per_market_max_orders", "updated_ts"}),
	}).Create(&store.StrategyConfigEntry{
		Strategy:           strategy,
		Enabled:            false,
		PerMarketMaxOrders: maxOrders,
		UpdatedTS:          time.Now().UnixMilli(),
	}).Error
}
