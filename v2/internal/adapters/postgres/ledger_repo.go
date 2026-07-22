package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/ledger"
	"gorm.io/gorm"
)

// LedgerRepo persists ledger entries and guards the pool balance atomically.
type LedgerRepo struct {
	db *gorm.DB
}

// NewLedgerRepo creates a ledger repository.
func NewLedgerRepo(db *gorm.DB) *LedgerRepo {
	return &LedgerRepo{db: db}
}

// poolLedgerRow maps to the pool_ledger table.
type poolLedgerRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement;column:id"`
	TS          int64  `gorm:"column:ts"`
	EntryType   string `gorm:"column:entry_type"`
	AmountCents int64  `gorm:"column:amount_cents"`
	OrderID     *int64 `gorm:"column:order_id"`
	Note        string `gorm:"column:note"`
}

func (poolLedgerRow) TableName() string { return "pool_ledger" }

// poolBalanceRow maps to the pool_balance table (singleton id=1).
type poolBalanceRow struct {
	ID           int   `gorm:"primaryKey;column:id"`
	BalanceCents int64 `gorm:"column:balance_cents"`
	UpdatedTS    int64 `gorm:"column:updated_ts"`
}

func (poolBalanceRow) TableName() string { return "pool_balance" }

// InitBalance seeds the singleton balance row if it doesn't exist and records
// the initial deposit as a ledger entry so that sum(entries) == balance.
func (r *LedgerRepo) InitBalance(ctx context.Context, initialCents int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`INSERT INTO pool_balance (id, balance_cents, updated_ts) VALUES (1, ?, ?)
			 ON CONFLICT (id) DO NOTHING`,
			initialCents, time.Now().UnixMilli(),
		)
		if res.Error != nil {
			return fmt.Errorf("init balance: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil // already exists
		}
		// Record the initial deposit so invariants hold.
		row := poolLedgerRow{
			TS:          time.Now().UnixMilli(),
			EntryType:   string(ledger.EntryDeposit),
			AmountCents: initialCents,
			Note:        "initial balance",
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("init balance: insert ledger: %w", err)
		}
		return nil
	})
}

// GetBalance returns the current balance in cents.
func (r *LedgerRepo) GetBalance(ctx context.Context) (int64, error) {
	var row poolBalanceRow
	if err := r.db.WithContext(ctx).Where("id = 1").First(&row).Error; err != nil {
		return 0, fmt.Errorf("get balance: %w", err)
	}
	return row.BalanceCents, nil
}

// HoldForOrder atomically deducts spendCents from the balance and records
// the hold. Returns ledger.ErrInsufficientBalance if the balance is too low.
func (r *LedgerRepo) HoldForOrder(ctx context.Context, orderID int64, spendCents int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE pool_balance SET balance_cents = balance_cents - ?, updated_ts = ?
			 WHERE id = 1 AND balance_cents >= ?`,
			spendCents, time.Now().UnixMilli(), spendCents,
		)
		if res.Error != nil {
			return fmt.Errorf("hold: update balance: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ledger.ErrInsufficientBalance
		}

		row := poolLedgerRow{
			TS:          time.Now().UnixMilli(),
			EntryType:   string(ledger.EntryOrderHold),
			AmountCents: -spendCents,
			OrderID:     &orderID,
			Note:        "hold for order",
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("hold: insert ledger: %w", err)
		}
		return nil
	})
}

// ReleaseHold credits releaseCents back and records the release.
func (r *LedgerRepo) ReleaseHold(ctx context.Context, orderID int64, releaseCents int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE pool_balance SET balance_cents = balance_cents + ?, updated_ts = ?
			 WHERE id = 1`,
			releaseCents, time.Now().UnixMilli(),
		)
		if res.Error != nil {
			return fmt.Errorf("release: update balance: %w", res.Error)
		}

		row := poolLedgerRow{
			TS:          time.Now().UnixMilli(),
			EntryType:   string(ledger.EntryHoldRelease),
			AmountCents: releaseCents,
			OrderID:     &orderID,
			Note:        "release hold",
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("release: insert ledger: %w", err)
		}
		return nil
	})
}

// RecordFill records the fill cost (actual cost of filled contracts).
// The fill cost is debited from the balance; the hold was already deducted.
// Net effect: balance -= fillCost, and the hold release covers the unfilled
// remainder separately.
//
// The ledger entry is negative (debit) so that sum(entries) == balance.
func (r *LedgerRepo) RecordFill(ctx context.Context, orderID int64, fillCostCents int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE pool_balance SET balance_cents = balance_cents - ?, updated_ts = ?
			 WHERE id = 1`,
			fillCostCents, time.Now().UnixMilli(),
		)
		if res.Error != nil {
			return fmt.Errorf("fill: update balance: %w", res.Error)
		}

		row := poolLedgerRow{
			TS:          time.Now().UnixMilli(),
			EntryType:   string(ledger.EntryFillCost),
			AmountCents: -fillCostCents,
			OrderID:     &orderID,
			Note:        "fill cost",
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("fill: insert ledger: %w", err)
		}
		return nil
	})
}

// RecordSettlement records a settlement payout (credit for a win).
func (r *LedgerRepo) RecordSettlement(ctx context.Context, orderID int64, payoutCents int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE pool_balance SET balance_cents = balance_cents + ?, updated_ts = ?
			 WHERE id = 1`,
			payoutCents, time.Now().UnixMilli(),
		)
		if res.Error != nil {
			return fmt.Errorf("settlement: update balance: %w", res.Error)
		}

		row := poolLedgerRow{
			TS:          time.Now().UnixMilli(),
			EntryType:   string(ledger.EntrySettlementPayout),
			AmountCents: payoutCents,
			OrderID:     &orderID,
			Note:        "settlement payout",
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("settlement: insert ledger: %w", err)
		}
		return nil
	})
}

// Deposit credits amountCents and records the deposit.
func (r *LedgerRepo) Deposit(ctx context.Context, amountCents int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE pool_balance SET balance_cents = balance_cents + ?, updated_ts = ?
			 WHERE id = 1`,
			amountCents, time.Now().UnixMilli(),
		)
		if res.Error != nil {
			return fmt.Errorf("deposit: update balance: %w", res.Error)
		}

		row := poolLedgerRow{
			TS:          time.Now().UnixMilli(),
			EntryType:   string(ledger.EntryDeposit),
			AmountCents: amountCents,
			Note:        "deposit",
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("deposit: insert ledger: %w", err)
		}
		return nil
	})
}

// GetEntries returns all ledger entries, ordered by timestamp.
func (r *LedgerRepo) GetEntries(ctx context.Context) ([]ledger.Entry, error) {
	var rows []poolLedgerRow
	if err := r.db.WithContext(ctx).Order("ts ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get entries: %w", err)
	}
	entries := make([]ledger.Entry, len(rows))
	for i, row := range rows {
		entries[i] = ledger.Entry{
			ID:          row.ID,
			TS:          row.TS,
			EntryType:   ledger.EntryType(row.EntryType),
			AmountCents: row.AmountCents,
			OrderID:     row.OrderID,
			Note:        row.Note,
		}
	}
	return entries, nil
}

// ErrNoBalance is returned when the balance row doesn't exist.
var ErrNoBalance = errors.New("pool balance row not found")

// CheckInvariants verifies that the ledger entries sum to the balance.
func (r *LedgerRepo) CheckInvariants(ctx context.Context) error {
	entries, err := r.GetEntries(ctx)
	if err != nil {
		return fmt.Errorf("get entries: %w", err)
	}
	bal, err := r.GetBalance(ctx)
	if err != nil {
		return fmt.Errorf("get balance: %w", err)
	}
	return ledger.CheckInvariants(entries, bal)
}
