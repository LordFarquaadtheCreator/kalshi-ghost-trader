// Package sizing implements Kelly criterion contract sizing in integer cents.
//
// Correct mode sizes in cents: edge * fraction * bankrollCents / priceCents,
// floored to a whole contract count. Legacy mode reproduces v1's
// dollars-as-contracts bug for backtest A/B comparison only.
package sizing

import "math"

// KellyContracts converts a fractional-Kelly allocation into whole contracts.
//
// probBps is the model probability in basis points (0..10000).
// priceCents is the per-contract price in cents (1..99).
// bankrollCents is the bankroll in cents.
// fraction is the Kelly fraction (e.g. 0.25).
// legacy=true reproduces v1's dollars-as-contracts bug.
//
// Returns 0 when edge ≤ 0, when priceCents is invalid, or when the bankroll
// can't afford a single contract.
func KellyContracts(probBps, priceCents int, bankrollCents int64, fraction float64, legacy bool) int {
	if priceCents <= 0 || bankrollCents <= 0 || fraction <= 0 {
		return 0
	}

	prob := float64(probBps) / 10000.0
	price := float64(priceCents) / 100.0

	// edge = (prob - price) / (1 - price)
	if price >= 1.0 {
		return 0
	}
	edge := (prob - price) / (1.0 - price)
	if edge <= 0 {
		return 0
	}

	if legacy {
		// v1 bug: sizes in dollars, treats dollar amount as contract count.
		// bankrollDollars = bankrollCents / 100; contracts = floor(fraction * edge * bankrollDollars)
		bankrollDollars := float64(bankrollCents) / 100.0
		return int(math.Floor(fraction * edge * bankrollDollars))
	}

	// Correct mode: size in cents, divide by priceCents to get contracts.
	// dollarsCents = fraction * edge * bankrollCents
	// contracts = floor(dollarsCents / priceCents)
	dollarsCents := fraction * edge * float64(bankrollCents)
	return int(math.Floor(dollarsCents / float64(priceCents)))
}

// SpendCents returns the exact integer-cent cost of buying `contracts`
// at `priceCents` per contract.
func SpendCents(contracts int, priceCents int) int64 {
	if contracts <= 0 || priceCents <= 0 {
		return 0
	}
	return int64(contracts) * int64(priceCents)
}
