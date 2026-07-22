// Package ledger defines the pure domain rules for the append-only pool
// ledger and guarded balance transitions.
//
// The ledger is append-only: every entry is a signed amount (holds negative,
// releases/payouts positive). The balance is the sum of all entries. The
// adapter (postgres) persists entries and guards the balance atomically; this
// package defines the rules and invariant checks.
package ledger

import (
	"errors"
	"fmt"
)

// EntryType is the type of a ledger entry.
type EntryType string

const (
	EntryDeposit          EntryType = "deposit"
	EntryWithdrawal       EntryType = "withdrawal"
	EntryOrderHold        EntryType = "order_hold"
	EntryHoldRelease      EntryType = "hold_release"
	EntryFillCost         EntryType = "fill_cost"
	EntrySettlementPayout EntryType = "settlement_payout"
)

// Entry is a single ledger row.
type Entry struct {
	ID          int64
	TS          int64
	EntryType   EntryType
	AmountCents int64
	OrderID     *int64
	Note        string
}

// ErrInsufficientBalance is returned when a hold would drive the balance negative.
var ErrInsufficientBalance = errors.New("insufficient balance for hold")

// Hold computes the new balance after placing a hold for spendCents.
// Returns ErrInsufficientBalance if balance < spendCents.
// Pure function — the adapter does the atomic DB update.
func Hold(balance, spendCents int64) (int64, error) {
	if spendCents <= 0 {
		return balance, nil
	}
	if balance < spendCents {
		return balance, ErrInsufficientBalance
	}
	return balance - spendCents, nil
}

// ReleaseHold computes the new balance after releasing a hold of releaseCents.
func ReleaseHold(balance, releaseCents int64) int64 {
	if releaseCents <= 0 {
		return balance
	}
	return balance + releaseCents
}

// RecordFill computes the new balance after recording a fill cost.
// Fill cost is the actual cost of the filled contracts (debit from balance).
// The ledger entry is negative so sum(entries) == balance.
func RecordFill(balance, fillCostCents int64) int64 {
	return balance - fillCostCents
}

// RecordSettlement computes the new balance after a settlement payout.
// Payout is positive (credit) for a win, zero for a loss.
func RecordSettlement(balance, payoutCents int64) int64 {
	if payoutCents <= 0 {
		return balance
	}
	return balance + payoutCents
}

// Deposit computes the new balance after a deposit.
func Deposit(balance, amountCents int64) int64 {
	if amountCents <= 0 {
		return balance
	}
	return balance + amountCents
}

// Withdraw computes the new balance after a withdrawal.
// Returns ErrInsufficientBalance if balance < amountCents.
func Withdraw(balance, amountCents int64) (int64, error) {
	if amountCents <= 0 {
		return balance, nil
	}
	if balance < amountCents {
		return balance, ErrInsufficientBalance
	}
	return balance - amountCents, nil
}

// CheckInvariants asserts that a sequence of ledger entries is consistent:
//  1. Sum of all amounts equals the final balance.
//  2. Every order_hold has at most one matching hold_release + fill_cost pair
//     whose combined release+fill does not exceed the hold amount.
func CheckInvariants(entries []Entry, balance int64) error {
	var sum int64
	holdsByOrder := make(map[int64]int64)   // order_id → total held
	releasesByOrder := make(map[int64]int64) // order_id → total released+filled

	for _, e := range entries {
		sum += e.AmountCents

		if e.OrderID == nil {
			if e.EntryType != EntryDeposit && e.EntryType != EntryWithdrawal {
				return fmt.Errorf("entry %d: type %s has nil order_id", e.ID, e.EntryType)
			}
			continue
		}

		oid := *e.OrderID
		switch e.EntryType {
		case EntryOrderHold:
			// Hold amounts are negative — store as positive.
			holdsByOrder[oid] += -e.AmountCents
		case EntryHoldRelease:
			// Release amounts are positive (credited back).
			releasesByOrder[oid] += e.AmountCents
		case EntryFillCost:
			// Fill cost amounts are negative (debit) — store as positive.
			releasesByOrder[oid] += -e.AmountCents
		case EntrySettlementPayout:
			// Payout is separate — not counted against hold.
		}
	}

	if sum != balance {
		return fmt.Errorf("sum of entries (%d) != balance (%d)", sum, balance)
	}

	for oid, held := range holdsByOrder {
		released := releasesByOrder[oid]
		if released > held {
			return fmt.Errorf("order %d: released+filled (%d) exceeds hold (%d)", oid, released, held)
		}
	}

	return nil
}
