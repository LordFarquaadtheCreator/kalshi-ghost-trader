package orders

// PaperFillModel provides realistic fill prices for paper orders.
// Buy fills cross the spread: fill at the best ask, not at last or mid.
// Cap fill size at displayed top-of-book size; remainder cancels (IOC).
// Apply fees and slippage.
type PaperFillModel interface {
	// FillPrice returns the fill price in cents for a buy at the given
	// best ask, plus the fee per contract and slippage penalty.
	// Returns (fillPriceCents, fillCount, feeCentsPerContract).
	FillPrice(bestAskCents int, requestedContracts int, displayedSize int) (fillPrice int, fillCount int, feePerContract int)
}

// RealisticPaperFill implements PaperFillModel with spread crossing,
// size capping, fees, and slippage (Addendum A.8).
type RealisticPaperFill struct {
	SlippageCents int // per-contract slippage penalty (default 1)
	FeeCents      int // per-contract fee
}

// NewRealisticPaperFill creates a realistic fill model.
func NewRealisticPaperFill(slippageCents, feeCents int) *RealisticPaperFill {
	if slippageCents < 0 {
		slippageCents = 1
	}
	return &RealisticPaperFill{
		SlippageCents: slippageCents,
		FeeCents:      feeCents,
	}
}

// FillPrice implements PaperFillModel.
// Buy fills cross the spread: fill at best ask + slippage.
// Cap fill size at displayed top-of-book size (IOC behavior).
func (m *RealisticPaperFill) FillPrice(bestAskCents int, requestedContracts int, displayedSize int) (int, int, int) {
	fillPrice := bestAskCents + m.SlippageCents

	fillCount := requestedContracts
	if displayedSize > 0 && fillCount > displayedSize {
		fillCount = displayedSize // IOC — remainder cancels
	}

	return fillPrice, fillCount, m.FeeCents
}

// OptimisticPaperFill fills at the intent price with no fees or slippage.
// This is the legacy v1 behavior, kept for comparison.
type OptimisticPaperFill struct{}

func (OptimisticPaperFill) FillPrice(bestAskCents int, requestedContracts int, displayedSize int) (int, int, int) {
	return bestAskCents, requestedContracts, 0
}
