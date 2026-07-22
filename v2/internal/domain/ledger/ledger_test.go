package ledger

import "testing"

func TestHoldInsufficient(t *testing.T) {
	_, err := Hold(100, 200)
	if err != ErrInsufficientBalance {
		t.Errorf("Hold(100, 200) err = %v, want ErrInsufficientBalance", err)
	}
}

func TestHoldOK(t *testing.T) {
	bal, err := Hold(1000, 300)
	if err != nil || bal != 700 {
		t.Errorf("Hold(1000, 300) = (%d, %v), want (700, nil)", bal, err)
	}
}

func TestHoldZero(t *testing.T) {
	bal, err := Hold(1000, 0)
	if err != nil || bal != 1000 {
		t.Errorf("Hold(1000, 0) = (%d, %v), want (1000, nil)", bal, err)
	}
}

func TestReleaseHold(t *testing.T) {
	if bal := ReleaseHold(700, 100); bal != 800 {
		t.Errorf("ReleaseHold(700, 100) = %d, want 800", bal)
	}
}

func TestRecordFill(t *testing.T) {
	if bal := RecordFill(800, 200); bal != 600 {
		t.Errorf("RecordFill(800, 200) = %d, want 600", bal)
	}
}

func TestRecordSettlement(t *testing.T) {
	if bal := RecordSettlement(600, 500); bal != 1100 {
		t.Errorf("RecordSettlement(600, 500) = %d, want 1100", bal)
	}
}

func TestDeposit(t *testing.T) {
	if bal := Deposit(1100, 400); bal != 1500 {
		t.Errorf("Deposit(1100, 400) = %d, want 1500", bal)
	}
}

func TestWithdrawInsufficient(t *testing.T) {
	_, err := Withdraw(100, 200)
	if err != ErrInsufficientBalance {
		t.Errorf("Withdraw(100, 200) err = %v, want ErrInsufficientBalance", err)
	}
}

func TestWithdrawOK(t *testing.T) {
	bal, err := Withdraw(1500, 300)
	if err != nil || bal != 1200 {
		t.Errorf("Withdraw(1500, 300) = (%d, %v), want (1200, nil)", bal, err)
	}
}

func TestCheckInvariantsOK(t *testing.T) {
	oid := int64Ptr(1)
	entries := []Entry{
		{ID: 1, TS: 1, EntryType: EntryDeposit, AmountCents: 10000},
		{ID: 2, TS: 2, EntryType: EntryOrderHold, AmountCents: -3000, OrderID: oid},
		{ID: 3, TS: 3, EntryType: EntryHoldRelease, AmountCents: 1000, OrderID: oid},
		{ID: 4, TS: 4, EntryType: EntryFillCost, AmountCents: -2000, OrderID: oid},
		{ID: 5, TS: 5, EntryType: EntrySettlementPayout, AmountCents: 4000, OrderID: oid},
	}
	// Sum = 10000 - 3000 + 1000 - 2000 + 4000 = 10000
	// hold=3000, release+fill=1000+2000=3000 — equals hold, OK.
	err := CheckInvariants(entries, 10000)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckInvariantsSumMismatch(t *testing.T) {
	entries := []Entry{
		{ID: 1, TS: 1, EntryType: EntryDeposit, AmountCents: 10000},
	}
	err := CheckInvariants(entries, 9999)
	if err == nil {
		t.Fatal("expected sum mismatch error")
	}
}

func TestCheckInvariantsReleaseExceedsHold(t *testing.T) {
	oid := int64Ptr(1)
	entries := []Entry{
		{ID: 1, TS: 1, EntryType: EntryDeposit, AmountCents: 10000},
		{ID: 2, TS: 2, EntryType: EntryOrderHold, AmountCents: -3000, OrderID: oid},
		{ID: 3, TS: 3, EntryType: EntryHoldRelease, AmountCents: 5000, OrderID: oid},
	}
	// Sum = 10000 - 3000 + 5000 = 12000
	err := CheckInvariants(entries, 12000)
	if err == nil {
		t.Fatal("expected release > hold error")
	}
}

func TestCheckInvariantsDepositWithdrawNoOrderID(t *testing.T) {
	entries := []Entry{
		{ID: 1, TS: 1, EntryType: EntryDeposit, AmountCents: 10000},
		{ID: 2, TS: 2, EntryType: EntryWithdrawal, AmountCents: -3000},
	}
	// Sum = 10000 - 3000 = 7000
	err := CheckInvariants(entries, 7000)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckInvariantsEntryWithNilOrderIDWrongType(t *testing.T) {
	entries := []Entry{
		{ID: 1, TS: 1, EntryType: EntryDeposit, AmountCents: 10000},
		{ID: 2, TS: 2, EntryType: EntryOrderHold, AmountCents: -3000}, // nil OrderID but wrong type
	}
	err := CheckInvariants(entries, 7000)
	if err == nil {
		t.Fatal("expected error for order_hold with nil order_id")
	}
}

func int64Ptr(v int64) *int64 { return &v }
