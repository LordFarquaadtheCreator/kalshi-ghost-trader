package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AppConfigKV is a single key-value pair from app_config.
type AppConfigKV struct {
	Key       string `gorm:"primaryKey;column:key"`
	Value     string `gorm:"column:value"`
	UpdatedTS int64  `gorm:"column:updated_ts"`
}

func (AppConfigKV) TableName() string { return "app_config" }

// GetAllAppConfig returns all key-value pairs from app_config.
func (d *DB) GetAllAppConfig(ctx context.Context) ([]AppConfigKV, error) {
	var pairs []AppConfigKV
	err := d.db.WithContext(ctx).Find(&pairs).Error
	return pairs, err
}

// GetAppConfig returns the value for a single key. Returns "" if not found.
func (d *DB) GetAppConfig(ctx context.Context, key string) (string, error) {
	var kv AppConfigKV
	err := d.db.WithContext(ctx).Where("key = ?", key).First(&kv).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return kv.Value, err
}

// DeleteAppConfig removes a key from app_config. No-op if key doesn't exist.
func (d *DB) DeleteAppConfig(ctx context.Context, key string) error {
	return d.db.WithContext(ctx).Where("key = ?", key).Delete(&AppConfigKV{}).Error
}

// SetAppConfig inserts or updates a key-value pair in app_config.
func (d *DB) SetAppConfig(ctx context.Context, key, value string) error {
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_ts"}),
	}).Create(&AppConfigKV{Key: key, Value: value, UpdatedTS: time.Now().UnixMilli()}).Error
}

// SetAppConfigBatch inserts or updates multiple key-value pairs in one transaction.
func (d *DB) SetAppConfigBatch(ctx context.Context, pairs []AppConfigKV) error {
	if len(pairs) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	for i := range pairs {
		pairs[i].UpdatedTS = now
	}
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_ts"}),
	}).CreateInBatches(&pairs, len(pairs)).Error
}

// LiquidityPool is the singleton row from liquidity_pool.
type LiquidityPool struct {
	ID                  int64  `gorm:"primaryKey;column:id"`
	BalanceCents        int64  `gorm:"column:balance_cents"`
	InitialBalanceCents int64  `gorm:"column:initial_balance_cents"`
	TotalSpentCents     int64  `gorm:"column:total_spent_cents"`
	TotalPNLCents       int64  `gorm:"column:total_pnl_cents"`
	UpdatedTS           int64  `gorm:"column:updated_ts"`
}

func (LiquidityPool) TableName() string { return "liquidity_pool" }

// GetLiquidityPool returns the liquidity pool state. Returns error if not initialized.
func (d *DB) GetLiquidityPool(ctx context.Context) (*LiquidityPool, error) {
	var lp LiquidityPool
	err := d.db.WithContext(ctx).Where("id = 1").First(&lp).Error
	if err != nil {
		return nil, fmt.Errorf("liquidity pool not initialized: %w", err)
	}
	return &lp, nil
}

// InitLiquidityPool seeds the liquidity pool with an initial balance (cents).
// No-op if already initialized.
func (d *DB) InitLiquidityPool(ctx context.Context, initialBalanceCents int64) error {
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&LiquidityPool{
		ID:                  1,
		BalanceCents:        initialBalanceCents,
		InitialBalanceCents: initialBalanceCents,
		UpdatedTS:           time.Now().UnixMilli(),
	}).Error
}

// ResetLiquidityPool resets the pool to a new initial balance.
func (d *DB) ResetLiquidityPool(ctx context.Context, initialBalanceCents int64) error {
	return d.db.WithContext(ctx).Model(&LiquidityPool{}).Where("id = 1").
		Updates(map[string]any{
			"balance_cents":        initialBalanceCents,
			"initial_balance_cents": initialBalanceCents,
			"total_spent_cents":    0,
			"total_pnl_cents":      0,
			"updated_ts":           time.Now().UnixMilli(),
		}).Error
}

// StrategyConfigEntry is one row from strategy_config.
type StrategyConfigEntry struct {
	Strategy  string `gorm:"primaryKey;column:strategy"`
	Enabled   bool   `gorm:"column:enabled"`
	UpdatedTS int64  `gorm:"column:updated_ts"`
}

func (StrategyConfigEntry) TableName() string { return "strategy_config" }

// GetAllStrategyConfig returns all strategy config entries.
func (d *DB) GetAllStrategyConfig(ctx context.Context) ([]StrategyConfigEntry, error) {
	var entries []StrategyConfigEntry
	err := d.db.WithContext(ctx).Find(&entries).Error
	return entries, err
}

// SetStrategyEnabled enables/disables a strategy for real trading.
// Inserts the row if it doesn't exist.
func (d *DB) SetStrategyEnabled(ctx context.Context, strategy string, enabled bool) error {
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "strategy"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_ts"}),
	}).Create(&StrategyConfigEntry{
		Strategy:  strategy,
		Enabled:   enabled,
		UpdatedTS: time.Now().UnixMilli(),
	}).Error
}

// IsStrategyEnabled returns whether a strategy is enabled for real trading.
// Returns false if the strategy has no config row (default disabled).
func (d *DB) IsStrategyEnabled(ctx context.Context, strategy string) (bool, error) {
	var e StrategyConfigEntry
	err := d.db.WithContext(ctx).Where("strategy = ?", strategy).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return e.Enabled, nil
}

// EnsureStrategyConfig inserts a strategy_config row if it doesn't exist (disabled by default).
func (d *DB) EnsureStrategyConfig(ctx context.Context, strategy string) error {
	return d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&StrategyConfigEntry{
		Strategy:  strategy,
		Enabled:   false,
		UpdatedTS: time.Now().UnixMilli(),
	}).Error
}

// TriggerRange is a price band for a strategy.
type TriggerRange struct {
	ID        int64   `gorm:"primaryKey;autoIncrement;column:id" json:"id,omitempty"`
	Strategy  string  `gorm:"column:strategy" json:"strategy,omitempty"`
	MinPrice  float64 `gorm:"column:min_price" json:"min_price"`
	MaxPrice  float64 `gorm:"column:max_price" json:"max_price"`
	Source    string  `gorm:"column:source" json:"source,omitempty"`
	Enabled   bool    `gorm:"column:enabled" json:"enabled"`
	CreatedTS int64   `gorm:"column:created_ts" json:"created_ts,omitempty"`
}

func (TriggerRange) TableName() string { return "strategy_trigger_ranges" }

// GetTriggerRanges returns all trigger ranges for a strategy.
func (d *DB) GetTriggerRanges(ctx context.Context, strategy string) ([]TriggerRange, error) {
	var ranges []TriggerRange
	err := d.db.WithContext(ctx).Where("strategy = ?", strategy).Order("created_ts").Find(&ranges).Error
	return ranges, err
}

// ReplaceTriggerRanges deletes all existing ranges for a strategy and inserts new ones.
func (d *DB) ReplaceTriggerRanges(ctx context.Context, strategy string, ranges []TriggerRange) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("strategy = ?", strategy).Delete(&TriggerRange{}).Error; err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		for i := range ranges {
			ranges[i].Strategy = strategy
			ranges[i].CreatedTS = now
		}
		if len(ranges) > 0 {
			return tx.CreateInBatches(&ranges, len(ranges)).Error
		}
		return nil
	})
}

// IsPriceInTriggerRange checks if a price falls within any enabled trigger range for a strategy.
func (d *DB) IsPriceInTriggerRange(ctx context.Context, strategy string, price float64) (bool, error) {
	var count int64
	err := d.db.WithContext(ctx).Model(&TriggerRange{}).
		Where("strategy = ? AND enabled = ? AND ? >= min_price AND ? <= max_price",
			strategy, true, price, price).Count(&count).Error
	return count > 0, err
}

// HasTriggerRanges returns true if a strategy has any trigger ranges configured (enabled or not).
func (d *DB) HasTriggerRanges(ctx context.Context, strategy string) (bool, error) {
	var count int64
	err := d.db.WithContext(ctx).Model(&TriggerRange{}).
		Where("strategy = ?", strategy).Count(&count).Error
	return count > 0, err
}

// DeductLiquidityPool atomically deducts spendCents from the pool balance
// and adds to total_spent_cents. Returns new balance in cents.
// Fails if insufficient balance (prevents going negative under concurrent access).
func (d *DB) DeductLiquidityPool(ctx context.Context, spendCents int64) (int64, error) {
	var newBalance int64
	err := d.db.WithContext(ctx).Raw(`
UPDATE liquidity_pool
SET balance_cents = balance_cents - ?,
    total_spent_cents = total_spent_cents + ?,
    updated_ts = ?
WHERE id = 1 AND balance_cents >= ?
RETURNING balance_cents`,
		spendCents, spendCents, time.Now().UnixMilli(), spendCents).Scan(&newBalance).Error
	if err == gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("insufficient liquidity pool balance for spend of %d cents", spendCents)
	}
	return newBalance, err
}

// RefundLiquidityPool atomically refunds spendCents to the pool balance
// and subtracts from total_spent_cents. Used when a real order fails
// after deduction but before execution.
func (d *DB) RefundLiquidityPool(ctx context.Context, refundCents int64) (int64, error) {
	var newBalance int64
	err := d.db.WithContext(ctx).Raw(`
UPDATE liquidity_pool
SET balance_cents = balance_cents + ?,
    total_spent_cents = MAX(total_spent_cents - ?, 0),
    updated_ts = ?
WHERE id = 1
RETURNING balance_cents`,
		refundCents, refundCents, time.Now().UnixMilli()).Scan(&newBalance).Error
	return newBalance, err
}
