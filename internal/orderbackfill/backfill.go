// Package orderbackfill polls Kalshi REST API for real orders that haven't
// reached a terminal status (resolved, failed, canceled). Fetches each
// order via GET /portfolio/orders/{order_id} and updates the DB with the
// latest status and fill count.
//
// This catches orders that were submitted but never got a WS user order
// update — e.g. WS disconnect, missed fill notification, or race condition
// between order submission and subscription.
package orderbackfill

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Backfill polls for unresolved real orders and fetches their status from Kalshi.
type Backfill struct {
	client *kalshiclient.Client
	db     *store.DB
	log    *slog.Logger
}

// New creates a Backfill.
func New(client *kalshiclient.Client, db *store.DB, log *slog.Logger) *Backfill {
	return &Backfill{client: client, db: db, log: log}
}

// Run polls for unresolved real orders at the given interval. Blocks until ctx cancelled.
func (b *Backfill) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	b.backfill(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			b.backfill(ctx)
		}
	}
}

func (b *Backfill) backfill(ctx context.Context) {
	orders, err := b.db.GetUnresolvedRealOrders(ctx)
	if err != nil {
		b.log.Error("orderbackfill: get unresolved orders", "err", err)
		return
	}
	if len(orders) == 0 {
		return
	}

	b.log.Info("orderbackfill: checking unresolved orders", "count", len(orders))

	updated := 0
	for _, o := range orders {
		if ctx.Err() != nil {
			return
		}

		od, err := b.client.GetOrder(ctx, o.KalshiOrderID)
		if err != nil {
			b.log.Warn("orderbackfill: fetch order failed",
				"order_id", o.KalshiOrderID, "err", err)
			continue
		}

		// Map Kalshi status to our internal status
		var internalStatus string
		switch od.Status {
		case "resting":
			internalStatus = "submitted"
		case "canceled":
			internalStatus = "canceled"
		case "executed":
			internalStatus = "filled"
		default:
			internalStatus = od.Status
		}

		// Skip if nothing changed
		if internalStatus == o.OrderStatus {
			continue
		}

		fillCount := parseFP(od.FillCountFP)

		if err := b.db.UpdateRealOrderStatus(ctx, o.ID, fillCount, internalStatus); err != nil {
			b.log.Error("orderbackfill: update order failed",
				"order_id", o.KalshiOrderID, "err", err)
			continue
		}

		updated++
		b.log.Info("orderbackfill: updated order",
			"order_id", o.KalshiOrderID,
			"old_status", o.OrderStatus,
			"new_status", internalStatus,
			"fill_count", fillCount)
	}

	if updated > 0 {
		b.log.Info("orderbackfill: pass complete", "checked", len(orders), "updated", updated)
	}
}

// parseFP parses a Kalshi fixed-point count string (e.g. "5.00") to float64.
func parseFP(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
