package algorithms

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
