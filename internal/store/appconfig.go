package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AppConfigKV is a single key-value pair from app_config.
type AppConfigKV struct {
	Key   string
	Value string
}

// GetAllAppConfig returns all key-value pairs from app_config.
func (d *DB) GetAllAppConfig(ctx context.Context) ([]AppConfigKV, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT key, value FROM app_config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairs []AppConfigKV
	for rows.Next() {
		var kv AppConfigKV
		if err := rows.Scan(&kv.Key, &kv.Value); err != nil {
			return nil, err
		}
		pairs = append(pairs, kv)
	}
	return pairs, rows.Err()
}

// GetAppConfig returns the value for a single key. Returns "" if not found.
func (d *DB) GetAppConfig(ctx context.Context, key string) (string, error) {
	var val string
	err := d.db.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// DeleteAppConfig removes a key from app_config. No-op if key doesn't exist.
func (d *DB) DeleteAppConfig(ctx context.Context, key string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM app_config WHERE key = ?", key)
	return err
}

// SetAppConfig inserts or updates a key-value pair in app_config.
func (d *DB) SetAppConfig(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO app_config (key, value, updated_ts) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_ts = excluded.updated_ts`,
		key, value, time.Now().UnixMilli())
	return err
}

// SetAppConfigBatch inserts or updates multiple key-value pairs in one transaction.
func (d *DB) SetAppConfigBatch(ctx context.Context, pairs []AppConfigKV) error {
	if len(pairs) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	for _, kv := range pairs {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO app_config (key, value, updated_ts) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_ts = excluded.updated_ts`,
			kv.Key, kv.Value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LiquidityPool is the singleton row from liquidity_pool.
type LiquidityPool struct {
	BalanceCents        int64
	InitialBalanceCents int64
	TotalSpentCents     int64
	TotalPNLCents       int64
	UpdatedTS           int64
}

// GetLiquidityPool returns the liquidity pool state. Returns error if not initialized.
func (d *DB) GetLiquidityPool(ctx context.Context) (*LiquidityPool, error) {
	var lp LiquidityPool
	err := d.db.QueryRowContext(ctx, `
SELECT balance_cents, initial_balance_cents, total_spent_cents, total_pnl_cents, updated_ts
FROM liquidity_pool WHERE id = 1`).Scan(
		&lp.BalanceCents, &lp.InitialBalanceCents, &lp.TotalSpentCents, &lp.TotalPNLCents, &lp.UpdatedTS)
	if err != nil {
		return nil, fmt.Errorf("liquidity pool not initialized: %w", err)
	}
	return &lp, nil
}

// InitLiquidityPool seeds the liquidity pool with an initial balance (cents).
// No-op if already initialized.
func (d *DB) InitLiquidityPool(ctx context.Context, initialBalanceCents int64) error {
	_, err := d.db.ExecContext(ctx, `
INSERT OR IGNORE INTO liquidity_pool (id, balance_cents, initial_balance_cents, total_spent_cents, total_pnl_cents, updated_ts)
VALUES (1, ?, ?, 0, 0, ?)`,
		initialBalanceCents, initialBalanceCents, time.Now().UnixMilli())
	return err
}

// ResetLiquidityPool resets the pool to a new initial balance.
func (d *DB) ResetLiquidityPool(ctx context.Context, initialBalanceCents int64) error {
	_, err := d.db.ExecContext(ctx, `
UPDATE liquidity_pool SET balance_cents = ?, initial_balance_cents = ?,
                           total_spent_cents = 0, total_pnl_cents = 0, updated_ts = ?
WHERE id = 1`,
		initialBalanceCents, initialBalanceCents, time.Now().UnixMilli())
	return err
}

// StrategyConfigEntry is one row from strategy_config.
type StrategyConfigEntry struct {
	Strategy  string
	Enabled   bool
	UpdatedTS int64
}

// GetAllStrategyConfig returns all strategy config entries.
func (d *DB) GetAllStrategyConfig(ctx context.Context) ([]StrategyConfigEntry, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT strategy, enabled, updated_ts FROM strategy_config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []StrategyConfigEntry
	for rows.Next() {
		var e StrategyConfigEntry
		var enabled int
		if err := rows.Scan(&e.Strategy, &enabled, &e.UpdatedTS); err != nil {
			return nil, err
		}
		e.Enabled = enabled != 0
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// SetStrategyEnabled enables/disables a strategy for real trading.
// Inserts the row if it doesn't exist.
func (d *DB) SetStrategyEnabled(ctx context.Context, strategy string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := d.db.ExecContext(ctx, `
INSERT INTO strategy_config (strategy, enabled, updated_ts) VALUES (?, ?, ?)
ON CONFLICT(strategy) DO UPDATE SET enabled = excluded.enabled, updated_ts = excluded.updated_ts`,
		strategy, e, time.Now().UnixMilli())
	return err
}

// IsStrategyEnabled returns whether a strategy is enabled for real trading.
// Returns false if the strategy has no config row (default disabled).
func (d *DB) IsStrategyEnabled(ctx context.Context, strategy string) (bool, error) {
	var enabled int
	err := d.db.QueryRowContext(ctx,
		"SELECT enabled FROM strategy_config WHERE strategy = ?", strategy).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled != 0, nil
}

// EnsureStrategyConfig inserts a strategy_config row if it doesn't exist (disabled by default).
func (d *DB) EnsureStrategyConfig(ctx context.Context, strategy string) error {
	_, err := d.db.ExecContext(ctx, `
INSERT OR IGNORE INTO strategy_config (strategy, enabled, updated_ts) VALUES (?, 0, ?)`,
		strategy, time.Now().UnixMilli())
	return err
}

// TriggerRange is a price band for a strategy.
type TriggerRange struct {
	ID        int64   `json:"id,omitempty"`
	Strategy  string  `json:"strategy,omitempty"`
	MinPrice  float64 `json:"min_price"`
	MaxPrice  float64 `json:"max_price"`
	Source    string  `json:"source,omitempty"` // 'peak' or 'manual'
	Enabled   bool    `json:"enabled"`
	CreatedTS int64   `json:"created_ts,omitempty"`
}

// GetTriggerRanges returns all trigger ranges for a strategy.
func (d *DB) GetTriggerRanges(ctx context.Context, strategy string) ([]TriggerRange, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT id, strategy, min_price, max_price, source, enabled, created_ts
FROM strategy_trigger_ranges WHERE strategy = ? ORDER BY created_ts`, strategy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ranges []TriggerRange
	for rows.Next() {
		var tr TriggerRange
		var enabled int
		if err := rows.Scan(&tr.ID, &tr.Strategy, &tr.MinPrice, &tr.MaxPrice, &tr.Source, &enabled, &tr.CreatedTS); err != nil {
			return nil, err
		}
		tr.Enabled = enabled != 0
		ranges = append(ranges, tr)
	}
	return ranges, rows.Err()
}

// ReplaceTriggerRanges deletes all existing ranges for a strategy and inserts new ones.
func (d *DB) ReplaceTriggerRanges(ctx context.Context, strategy string, ranges []TriggerRange) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM strategy_trigger_ranges WHERE strategy = ?", strategy); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	for _, r := range ranges {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO strategy_trigger_ranges (strategy, min_price, max_price, source, enabled, created_ts)
VALUES (?, ?, ?, ?, ?, ?)`,
			strategy, r.MinPrice, r.MaxPrice, r.Source, boolToInt(r.Enabled), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsPriceInTriggerRange checks if a price falls within any enabled trigger range for a strategy.
func (d *DB) IsPriceInTriggerRange(ctx context.Context, strategy string, price float64) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM strategy_trigger_ranges
WHERE strategy = ? AND enabled = 1 AND ? >= min_price AND ? <= max_price`,
		strategy, price, price).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// HasTriggerRanges returns true if a strategy has any trigger ranges configured (enabled or not).
func (d *DB) HasTriggerRanges(ctx context.Context, strategy string) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM strategy_trigger_ranges WHERE strategy = ?", strategy).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DeductLiquidityPool atomically deducts spendCents from the pool balance
// and adds to total_spent_cents. Returns new balance in cents.
// Fails if insufficient balance (prevents going negative under concurrent access).
func (d *DB) DeductLiquidityPool(ctx context.Context, spendCents int64) (int64, error) {
	var newBalance int64
	err := d.db.QueryRowContext(ctx, `
UPDATE liquidity_pool
SET balance_cents = balance_cents - ?,
    total_spent_cents = total_spent_cents + ?,
    updated_ts = ?
WHERE id = 1 AND balance_cents >= ?
RETURNING balance_cents`,
		spendCents, spendCents, time.Now().UnixMilli(), spendCents).Scan(&newBalance)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("insufficient liquidity pool balance for spend of %d cents", spendCents)
	}
	return newBalance, err
}

// RefundLiquidityPool atomically refunds spendCents to the pool balance
// and subtracts from total_spent_cents. Used when a real order fails
// after deduction but before execution.
func (d *DB) RefundLiquidityPool(ctx context.Context, refundCents int64) (int64, error) {
	var newBalance int64
	err := d.db.QueryRowContext(ctx, `
UPDATE liquidity_pool
SET balance_cents = balance_cents + ?,
    total_spent_cents = MAX(total_spent_cents - ?, 0),
    updated_ts = ?
WHERE id = 1
RETURNING balance_cents`,
		refundCents, refundCents, time.Now().UnixMilli()).Scan(&newBalance)
	return newBalance, err
}
