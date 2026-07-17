package algorithms

// Package-level sizing params, set from config via SetSizingParams.
var (
	paperBankroll  float64 = 1000
	kellyFractionP float64 = 0.25
)

// SetSizingParams sets the global bankroll and Kelly fraction used by all strategies.
func SetSizingParams(bankroll, fraction float64) {
	paperBankroll = bankroll
	kellyFractionP = fraction
}

// kellySize computes order size using fractional Kelly criterion.
// fKelly = (convProb - marketPrice) / (1 - marketPrice)
// size = kellyFraction * fKelly * bankroll
//
// Returns 0 if inputs are invalid or edge is non-positive.
func kellySize(convProb, marketPrice, bankroll, kellyFraction float64) float64 {
	if bankroll <= 0 || kellyFraction <= 0 || marketPrice >= 1 || marketPrice <= 0 {
		return 0
	}
	fKelly := (convProb - marketPrice) / (1 - marketPrice)
	if fKelly <= 0 {
		return 0
	}
	size := kellyFraction * fKelly * bankroll
	// cap total cost at $5 (size * price)
	maxSize := 5.0 / marketPrice
	if size > maxSize {
		size = maxSize
	}
	return size
}

// kellySized is a convenience that uses the package-level sizing params.
func kellySized(convProb, marketPrice float64) float64 {
	return kellySize(convProb, marketPrice, paperBankroll, kellyFractionP)
}

// KellySizedExported is the exported version for cross-package use (e.g. signal.CloseTimer).
func KellySizedExported(convProb, marketPrice float64) float64 {
	return kellySize(convProb, marketPrice, paperBankroll, kellyFractionP)
}

// GetPaperBankroll returns the current paper bankroll used for sizing.
func GetPaperBankroll() float64 { return paperBankroll }

// GetKellyFraction returns the current Kelly fraction used for sizing.
func GetKellyFraction() float64 { return kellyFractionP }
