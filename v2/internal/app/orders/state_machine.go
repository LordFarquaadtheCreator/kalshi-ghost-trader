// Package orders implements the order state machine, gate cache, and
// asynchronous submission worker. Intents from the match loop flow through
// gates → sizing → ledger hold → exchange submission → fill/cancel.
package orders

import "errors"

// Order statuses.
const (
	StatusIntent    = "intent"
	StatusGated     = "gated"
	StatusAccepted  = "accepted"
	StatusHeld      = "held"
	StatusSubmitted = "submitted"
	StatusFilled    = "filled"
	StatusPartial   = "partial"
	StatusCanceled  = "canceled"
	StatusFailed    = "failed"
	StatusUnverified = "unverified"
	StatusSettled   = "settled"
)

// Gate reasons (in evaluation order).
const (
	GateRealTradingDisabled = "real_trading_disabled"
	GateStrategyDisabled    = "strategy_disabled"
	GatePerMarketLimit      = "per_market_limit"
	GatePriceBand           = "price_band"
	GateQuota               = "quota"
	GateCooldown            = "cooldown"
	GatePreMatch            = "pre_match"
	GateInsufficientBalance = "insufficient_balance"
)

// legalTransitions defines the state machine: from → set of allowed targets.
var legalTransitions = map[string]map[string]bool{
	StatusIntent: {
		StatusGated:    true,
		StatusAccepted: true,
	},
	StatusGated: {
		// Terminal — no further transitions from gated.
	},
	StatusAccepted: {
		StatusHeld:    true,
		StatusFilled:  true, // paper path: accepted → filled
		StatusGated:   true, // insufficient balance discovered after accept
	},
	StatusHeld: {
		StatusSubmitted:  true,
		StatusFailed:     true,
	},
	StatusSubmitted: {
		StatusFilled:    true,
		StatusPartial:   true,
		StatusCanceled:  true,
		StatusUnverified: true,
		StatusFailed:    true,
	},
	StatusPartial: {
		StatusFilled:  true,
		StatusCanceled: true,
		StatusSettled:  true,
	},
	StatusUnverified: {
		StatusFilled:   true,
		StatusPartial:  true,
		StatusCanceled: true,
	},
	StatusFilled: {
		StatusSettled: true,
	},
	StatusCanceled: {
		// Terminal.
	},
	StatusFailed: {
		// Terminal.
	},
	StatusSettled: {
		// Terminal.
	},
}

// ErrIllegalTransition is returned when a status transition is not allowed.
var ErrIllegalTransition = errors.New("illegal order status transition")

// IsLegalTransition returns true if from → to is a legal transition.
func IsLegalTransition(from, to string) bool {
	targets, ok := legalTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// AllStatuses returns all valid order statuses.
func AllStatuses() []string {
	return []string{
		StatusIntent, StatusGated, StatusAccepted, StatusHeld,
		StatusSubmitted, StatusFilled, StatusPartial, StatusCanceled,
		StatusFailed, StatusUnverified, StatusSettled,
	}
}
