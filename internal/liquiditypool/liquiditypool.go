package liquiditypool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInsufficientBalance is returned when a deduction exceeds the pool balance.
var ErrInsufficientBalance = errors.New("insufficient liquidity pool balance")

// ErrPoolMissing is returned when the pool row (id=1) is absent — the singleton
// has not been initialized. Returned by Refund/TopUp/Credit when the UPDATE
// matches zero rows.
var ErrPoolMissing = errors.New("liquidity pool row missing")

// Get returns the liquidity pool state. Returns error if not initialized.
func Get(ctx context.Context, db *gorm.DB) (*store.LiquidityPool, error) {
	var lp store.LiquidityPool
	err := db.WithContext(ctx).Where("id = 1").First(&lp).Error
	if err != nil {
		return nil, fmt.Errorf("liquidity pool not initialized: %w", err)
	}
	return &lp, nil
}

// Init seeds the liquidity pool with an initial balance (cents).
// No-op if already initialized.
func Init(ctx context.Context, db *gorm.DB, initialBalanceCents int64) error {
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&store.LiquidityPool{
		ID:                  1,
		BalanceCents:        initialBalanceCents,
		InitialBalanceCents: initialBalanceCents,
		UpdatedTS:           time.Now().UnixMilli(),
	}).Error
}

// Reset resets the pool to a new initial balance.
// Wipes balance, initial_balance, total_spent, total_pnl. Use when changing
// the risk envelope — e.g. "I want to risk $20 now, fresh start".
func Reset(ctx context.Context, db *gorm.DB, initialBalanceCents int64) error {
	return db.WithContext(ctx).Model(&store.LiquidityPool{}).Where("id = 1").
		Updates(map[string]any{
			"balance_cents":         initialBalanceCents,
			"initial_balance_cents": initialBalanceCents,
			"total_spent_cents":     0,
			"total_pnl_cents":       0,
			"updated_ts":            time.Now().UnixMilli(),
		}).Error
}

// TopUp adds capital to the pool without wiping history.
// Increases balance_cents and initial_balance_cents by addCents so P&L %
// stays meaningful against the new contribution baseline. Use when injecting
// more capital mid-run without resetting the track record.
// Returns ErrPoolMissing if the singleton row (id=1) is absent.
func TopUp(ctx context.Context, db *gorm.DB, addCents int64) error {
	res := db.WithContext(ctx).Exec(`
UPDATE liquidity_pool
SET balance_cents = balance_cents + ?,
    initial_balance_cents = initial_balance_cents + ?,
    updated_ts = ?
WHERE id = 1`,
		addCents, addCents, time.Now().UnixMilli())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPoolMissing
	}
	return nil
}

// Deduct atomically deducts spendCents from the pool balance
// and adds to total_spent_cents. Returns new balance in cents.
// Returns ErrInsufficientBalance when the spend exceeds the current balance
// (detected via RowsAffected == 0 on the guarded UPDATE, not via a sentinel
// error — the previous ErrRecordNotFound comparison never fired because
// Raw().Scan() on a no-row UPDATE returns no error).
func Deduct(ctx context.Context, db *gorm.DB, spendCents int64) (int64, error) {
	var newBalance int64
	res := db.WithContext(ctx).Raw(`
UPDATE liquidity_pool
SET balance_cents = balance_cents - ?,
    total_spent_cents = total_spent_cents + ?,
    updated_ts = ?
WHERE id = 1 AND balance_cents >= ?
RETURNING balance_cents`,
		spendCents, spendCents, time.Now().UnixMilli(), spendCents).Scan(&newBalance)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, fmt.Errorf("%w: spend of %d cents", ErrInsufficientBalance, spendCents)
	}
	return newBalance, nil
}

// Refund atomically refunds spendCents to the pool balance
// and subtracts from total_spent_cents. Used when a real order fails
// after deduction but before execution.
// Returns ErrPoolMissing if the singleton row (id=1) is absent.
func Refund(ctx context.Context, db *gorm.DB, refundCents int64) (int64, error) {
	var newBalance int64
	res := db.WithContext(ctx).Raw(`
UPDATE liquidity_pool
SET balance_cents = balance_cents + ?,
    total_spent_cents = MAX(total_spent_cents - ?, 0),
    updated_ts = ?
WHERE id = 1
RETURNING balance_cents`,
		refundCents, refundCents, time.Now().UnixMilli()).Scan(&newBalance)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, ErrPoolMissing
	}
	return newBalance, nil
}

// Credit atomically adds proceedsCents to the pool balance.
// Used for sell-to-close fills: selling N contracts at price p credits
// N*p*100 cents to the pool. Does NOT touch total_spent_cents (that
// tracks buy-side capital deployed, not sell-side proceeds).
// Realized P&L from the trade is tracked on the position row, not here.
// Returns ErrPoolMissing if the singleton row (id=1) is absent.
func Credit(ctx context.Context, db *gorm.DB, proceedsCents int64) (int64, error) {
	var newBalance int64
	res := db.WithContext(ctx).Raw(`
UPDATE liquidity_pool
SET balance_cents = balance_cents + ?,
    updated_ts = ?
WHERE id = 1
RETURNING balance_cents`,
		proceedsCents, time.Now().UnixMilli()).Scan(&newBalance)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, ErrPoolMissing
	}
	return newBalance, nil
}
