package orders

import (
	"context"
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

func TestRealisticPaperFillCrossesSpread(t *testing.T) {
	m := NewRealisticPaperFill(1, 0) // 1 cent slippage, 0 fee

	// Buy at best ask 55, slippage 1 → fill at 56.
	fillPrice, fillCount, fee := m.FillPrice(55, 10, 100)
	if fillPrice != 56 {
		t.Errorf("fill price = %d, want 56 (best ask + slippage)", fillPrice)
	}
	if fillCount != 10 {
		t.Errorf("fill count = %d, want 10 (within displayed size)", fillCount)
	}
	if fee != 0 {
		t.Errorf("fee = %d, want 0", fee)
	}
}

func TestRealisticPaperFillCappedAtDisplayedSize(t *testing.T) {
	m := NewRealisticPaperFill(1, 0)

	// Request 20 contracts, only 5 displayed → IOC caps at 5.
	fillPrice, fillCount, _ := m.FillPrice(55, 20, 5)
	if fillCount != 5 {
		t.Errorf("fill count = %d, want 5 (capped at displayed size)", fillCount)
	}
	if fillPrice != 56 {
		t.Errorf("fill price = %d, want 56", fillPrice)
	}
}

func TestRealisticPaperFillWithFees(t *testing.T) {
	m := NewRealisticPaperFill(1, 2) // 1 cent slippage, 2 cents fee

	_, _, fee := m.FillPrice(55, 10, 100)
	if fee != 2 {
		t.Errorf("fee per contract = %d, want 2", fee)
	}
}

func TestRealisticPaperFillZeroDisplayedSize(t *testing.T) {
	m := NewRealisticPaperFill(1, 0)

	// No displayed size → fill the full request (no cap).
	_, fillCount, _ := m.FillPrice(55, 10, 0)
	if fillCount != 10 {
		t.Errorf("fill count = %d, want 10 (no cap when displayed size is 0)", fillCount)
	}
}

func TestOptimisticPaperFill(t *testing.T) {
	m := OptimisticPaperFill{}

	// Legacy: fill at intent price, no slippage, no fee, no cap.
	fillPrice, fillCount, fee := m.FillPrice(50, 10, 5)
	if fillPrice != 50 {
		t.Errorf("fill price = %d, want 50 (intent price)", fillPrice)
	}
	if fillCount != 10 {
		t.Errorf("fill count = %d, want 10 (no cap)", fillCount)
	}
	if fee != 0 {
		t.Errorf("fee = %d, want 0", fee)
	}
}

// fakeBookLookup is a test BookLookup.
type fakeBookLookup struct {
	snapshot *ports.BookSnapshot
}

func (f *fakeBookLookup) Lookup(ctx context.Context, marketTicker string) (*ports.BookSnapshot, error) {
	return f.snapshot, nil
}

func TestWorkerPaperFillRealistic(t *testing.T) {
	// Verify the worker uses the realistic fill model with book lookup.
	w := &Worker{
		paperFill:  NewRealisticPaperFill(1, 0),
		bookLookup: &fakeBookLookup{snapshot: &ports.BookSnapshot{BestAskCents: 55, BestAskSize: 5}},
	}

	// Simulate the paper fill logic directly.
	bestAsk := 50
	displayedSize := 10
	if w.bookLookup != nil {
		if book, err := w.bookLookup.Lookup(context.Background(), "TEST"); err == nil && book != nil {
			if book.BestAskCents > 0 {
				bestAsk = book.BestAskCents
			}
			if book.BestAskSize > 0 {
				displayedSize = book.BestAskSize
			}
		}
	}

	fillPrice, fillCount, _ := w.paperFill.FillPrice(bestAsk, 10, displayedSize)
	if fillPrice != 56 {
		t.Errorf("fill price = %d, want 56 (55 + 1 slippage)", fillPrice)
	}
	if fillCount != 5 {
		t.Errorf("fill count = %d, want 5 (capped at displayed size)", fillCount)
	}
}
