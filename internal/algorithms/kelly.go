package algorithms

import "github.com/farquaad/kalshi-ghost-trader/internal/config"

// Package-level sizing params, set from config via SetSizingParams.
// Real order sizing reads liquidity_pool.balance_cents live (see real_emitter.go),
// not a static bankroll — profit compounds, losses shrink sizing automatically.
var (
	paperBankroll  float64 = 1000
	kellyFractionP float64 = 0.25
)

// SetSizingParams reads paper bankroll and Kelly fraction from config.Cfg.
func SetSizingParams() {
	paperBankroll = config.Cfg.PaperBankroll
	kellyFractionP = config.Cfg.KellyFraction
}

// kellySizeRaw computes order size using fractional Kelly criterion without cost cap.
// fKelly = (convProb - marketPrice) / (1 - marketPrice)
// size = kellyFraction * fKelly * bankroll
//
// Returns 0 if inputs are invalid or edge is non-positive.
func kellySizeRaw(convProb, marketPrice, bankroll, kellyFraction float64) float64 {
	if bankroll <= 0 || kellyFraction <= 0 || marketPrice >= 1 || marketPrice <= 0 {
		return 0
	}
	fKelly := (convProb - marketPrice) / (1 - marketPrice)
	if fKelly <= 0 {
		return 0
	}
	return kellyFraction * fKelly * bankroll
}

// kellySize computes order size with a $5 cost cap (paper trading safety).
func kellySize(convProb, marketPrice, bankroll, kellyFraction float64) float64 {
	size := kellySizeRaw(convProb, marketPrice, bankroll, kellyFraction)
	if size <= 0 {
		return 0
	}
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
