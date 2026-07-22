package algorithms

import (
	"context"
	"log/slog"

	"github.com/farquaad/kalshi-ghost-trader/internal/positions"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// PaperPositionEmitter wraps an OrderEmitter and applies position lifecycle
// for paper (simulated) orders before forwarding. Mirrors the position
// tracking that KalshiOrderEmitter does for real orders.
//
// Paper sells assume full fill at SuggestedSize (no real orderbook to
// reject). Buys also assume full fill. This matches the existing
// ResolveSimulatedOrders assumption of full fill at SuggestedSize.
//
// For paper orders, position updates happen synchronously before the
// order is forwarded to inner. If position update fails (e.g. sell with
// no open position), the order is dropped — mirrors real emitter rejecting
// naked shorts.
type PaperPositionEmitter struct {
	inner OrderEmitter
	pos   *positions.Manager
	log   *slog.Logger
}

// NewPaperPositionEmitter wraps inner with paper position tracking.
func NewPaperPositionEmitter(inner OrderEmitter, db *store.DB, log *slog.Logger) *PaperPositionEmitter {
	return &PaperPositionEmitter{
		inner: inner,
		pos:   positions.New(db.GormDB()),
		log:   log,
	}
}

func (e *PaperPositionEmitter) EmitOrder(o store.Order) bool {
	ctx := context.Background()

	// Default side for legacy orders.
	if o.Side == "" {
		if o.Action == "sell" {
			o.Side = store.OrderSideClose
		} else {
			o.Side = store.OrderSideOpen
		}
	}

	// Paper assumes full fill at SuggestedSize. If SuggestedSize is zero
	// (shouldn't happen but guard), skip position tracking and just forward.
	fillCount := o.SuggestedSize

	switch o.Side {
	case store.OrderSideClose:
		// Sell-to-close: apply to position. Reject if no open position.
		if fillCount <= 0 {
			// Nothing to sell — forward as-is for paper trail, no position update.
			return e.inner.EmitOrder(o)
		}
		posID, realizedPnL, remaining, err := e.pos.ApplySell(
			ctx, o.MatchTicker, o.MarketTicker, o.Strategy, false, fillCount, o.MarketPrice)
		if err != nil {
			e.log.Warn("paper: sell rejected by position manager",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"size", fillCount, "error", err)
			return false
		}
		o.PositionID = &posID
		e.log.Info("paper: position closed",
			"market", o.MarketTicker, "strategy", o.Strategy,
			"fill_count", fillCount, "realized_pnl_cents", realizedPnL,
			"remaining_open", remaining)
	default:
		// Buy-to-open: apply to position.
		if fillCount > 0 {
			posID, err := e.pos.ApplyBuy(
				ctx, o.MatchTicker, o.MarketTicker, o.Strategy, false, fillCount, o.MarketPrice)
			if err != nil {
				e.log.Warn("paper: buy position update failed",
					"market", o.MarketTicker, "strategy", o.Strategy,
					"size", fillCount, "error", err)
				// Don't drop — still forward for paper trail. Position is best-effort.
			} else {
				o.PositionID = &posID
			}
		}
	}

	return e.inner.EmitOrder(o)
}

var _ OrderEmitter = (*PaperPositionEmitter)(nil)
