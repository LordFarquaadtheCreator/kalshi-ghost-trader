package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// InsertOrdersBatch inserts a batch of orders in one transaction.
func (d *DB) InsertOrdersBatch(ctx context.Context, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).CreateInBatches(&orders, len(orders)).Error
}

// DenormalizeResultToOrders copies the market result onto every unsettled
// order row for that market. Lets the paper-orders route filter and aggregate
// without joining markets. Idempotent — only fills rows where result IS NULL.
// No-op when result is empty.
func (d *DB) DenormalizeResultToOrders(ctx context.Context, marketTicker, result string, settledTS int64) error {
	if result == "" {
		return nil
	}
	return d.db.WithContext(ctx).Exec(`
UPDATE orders SET result = ?, settled_ts = ?
WHERE market_ticker = ? AND (result IS NULL OR result = '')`,
		result, settledTS, marketTicker).Error
}

// GetOrders returns all simulated orders, ordered by timestamp.
func (d *DB) GetOrders(ctx context.Context) ([]Order, error) {
	var orders []Order
	err := d.db.WithContext(ctx).Order("ts").Find(&orders).Error
	return orders, err
}

// InsertRealOrder inserts a single real order and returns the autoincrement ID.
// Used by KalshiOrderEmitter which needs the ID for subsequent status updates.
func (d *DB) InsertRealOrder(ctx context.Context, o Order) (int64, error) {
	o.IsReal = true
	res := d.db.WithContext(ctx).Create(&o)
	if res.Error != nil {
		return 0, res.Error
	}
	return o.ID, nil
}

// UpdateRealOrder updates an existing order row with Kalshi response fields.
// Called by KalshiOrderEmitter after order submission. Refunds unfilled portion
// for buy/buy_no partial fills and zero-fill cancels so pool stays accurate.
// Sells (Action="sell") never deduct from pool — skip refund for sell cancels
// to avoid inflating the pool with money never taken.
// reason is appended to Context when status is canceled — explains why.
func (d *DB) UpdateRealOrder(ctx context.Context, orderID int64, kalshiOrderID string, fillCount float64, status string, reason string) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o Order
		if err := tx.Where("id = ?", orderID).First(&o).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"is_real":         true,
			"kalshi_order_id": kalshiOrderID,
			"fill_count":      fillCount,
			"order_status":    status,
		}
		if status == "canceled" && reason != "" {
			updates["context"] = appendContextReason(o.Context, reason)
		}
		// Sells never deduct — refunding would inflate the pool.
		if o.Action != "sell" {
			refunded, newBalance, err := refundUnfilled(tx, &o, fillCount)
			if err != nil {
				return err
			}
			updates["unfilled_refunded_cents"] = o.UnfilledRefundedCents
			if refunded > 0 {
				updates["pool_balance_before_cents"] = newBalance - refunded
				updates["pool_balance_after_cents"] = newBalance
			}
		}
		return tx.Model(&Order{}).Where("id = ?", orderID).Updates(updates).Error
	})
}

// MarkRealOrderFailed marks an order as failed and refunds the full deducted
// amount to the liquidity pool. Use ONLY when the pool was actually deducted
// (buy/buy_no submit failures after successful Deduct). reason is appended to
// Context — explains why the order failed.
func (d *DB) MarkRealOrderFailed(ctx context.Context, orderID int64, reason string) error {
	return d.markRealOrderFailed(ctx, orderID, reason, true)
}

// MarkRealOrderFailedNoRefund marks an order as failed without touching the
// pool. Use when the pool was NOT deducted — deduct failures (insufficient
// balance) and sell submit failures (sells never deduct; they credit on fill).
// Refunding in those cases inflates the pool with money never taken.
func (d *DB) MarkRealOrderFailedNoRefund(ctx context.Context, orderID int64, reason string) error {
	return d.markRealOrderFailed(ctx, orderID, reason, false)
}

func (d *DB) markRealOrderFailed(ctx context.Context, orderID int64, reason string, refund bool) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o Order
		if err := tx.Where("id = ?", orderID).First(&o).Error; err != nil {
			return err
		}
		if o.OrderStatus == "failed" || o.OrderStatus == "canceled" || o.OrderStatus == "resolved" {
			return nil // idempotent
		}
		updates := map[string]any{
			"is_real":      true,
			"order_status": "failed",
		}
		if reason != "" {
			updates["context"] = appendContextReason(o.Context, reason)
		}
		if refund {
			refunded, newBalance, err := refundUnfilled(tx, &o, 0) // fillCount=0, full refund
			if err != nil {
				return err
			}
			updates["unfilled_refunded_cents"] = o.UnfilledRefundedCents
			if refunded > 0 {
				updates["pool_balance_before_cents"] = newBalance - refunded
				updates["pool_balance_after_cents"] = newBalance
			}
		}
		return tx.Model(&Order{}).Where("id = ?", orderID).Updates(updates).Error
	})
}

// appendContextReason appends a failure/cancel reason to the existing context
// string. Format: "original_context | reason: <reason>". If context is empty,
// starts with "reason: <reason>".
func appendContextReason(existing, reason string) string {
	if existing == "" {
		return "reason: " + reason
	}
	return existing + " | reason: " + reason
}

// GetRealOrders returns all real orders (is_real=1), ordered by timestamp.
// Optional fromTS/toTS filter by order timestamp (unix millis, inclusive).
func (d *DB) GetRealOrders(ctx context.Context) ([]Order, error) {
	var orders []Order
	err := d.db.WithContext(ctx).Where("is_real = ?", true).Order("ts DESC").Find(&orders).Error
	return orders, err
}

// GetRealOrdersRange returns real orders within a timestamp range.
func (d *DB) GetRealOrdersRange(ctx context.Context, fromTS, toTS int64) ([]Order, error) {
	q := d.db.WithContext(ctx).Where("is_real = ?", true)
	if fromTS > 0 {
		q = q.Where("ts >= ?", fromTS)
	}
	if toTS > 0 {
		q = q.Where("ts <= ?", toTS)
	}
	var orders []Order
	err := q.Order("ts DESC").Find(&orders).Error
	return orders, err
}

// CountRealOrdersByMarketStrategy counts real orders for a (market, strategy) pair
// that actually filled (fill_count > 0). Canceled/failed/zero-fill orders don't count
// — allows retries after cancels. Used by KalshiOrderEmitter per-market strategy limit guard.
func (d *DB) CountRealOrdersByMarketStrategy(ctx context.Context, marketTicker, strategy string) (int64, error) {
	var count int64
	err := d.db.WithContext(ctx).Model(&Order{}).
		Where("is_real = ? AND market_ticker = ? AND strategy = ? AND fill_count > 0",
			true, marketTicker, strategy).
		Count(&count).Error
	return count, err
}

// ResolveRealOrders resolves all real orders for a settled market.
// Pool adjustment: refund unfilled portion cost + add gross payout ($1/contract if won).
// resolved_pnl_cents = payout - filled_cost (P&L on filled contracts only).
func (d *DB) ResolveRealOrders(ctx context.Context, marketTicker, result string) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Fetch unresolved real orders with suggested_size for cost computation
		var pendingOrders []Order
		err := tx.Where("is_real = ? AND market_ticker = ? AND order_status IN ?",
			true, marketTicker, []string{"submitted", "filled", "partial"}).
			Find(&pendingOrders).Error
		if err != nil {
			return err
		}

		if len(pendingOrders) == 0 {
			return nil
		}

		// Get current pool balance
		var lp LiquidityPool
		if err := tx.Where("id = 1").First(&lp).Error; err != nil {
			return fmt.Errorf("resolve: read liquidity pool: %w", err)
		}
		poolBalance := lp.BalanceCents

		for _, po := range pendingOrders {
			// buy_no wins when result is "no", not "yes"
			orderWon := result == "yes"
			if po.Action == "buy_no" {
				orderWon = result == "no"
			}
			// refund unfilled portion, minus what was already refunded at submit/cancel time
			totalUnfilledCents := int64((po.SuggestedSize - po.FillCount) * po.MarketPrice * 100)
			if totalUnfilledCents < 0 {
				totalUnfilledCents = 0
			}
			unfilledRefundCents := totalUnfilledCents - po.UnfilledRefundedCents
			if unfilledRefundCents < 0 {
				unfilledRefundCents = 0
			}

			// gross payout: $1 per contract if won, $0 if lost
			var payoutCents int64
			var pnlCents int64
			if po.FillCount > 0 && orderWon {
				payoutCents = int64(po.FillCount * 100)
				pnlCents = payoutCents - int64(po.FillCount*po.MarketPrice*100)
			} else if po.FillCount > 0 {
				pnlCents = -int64(po.FillCount * po.MarketPrice * 100)
			}
			// zero fill: pnlCents = 0, full refund via unfilledRefundCents

			poolAdjustment := unfilledRefundCents + payoutCents
			before := poolBalance
			poolBalance += poolAdjustment

			if err := tx.Model(&Order{}).Where("id = ?", po.ID).
				Updates(map[string]any{
					"order_status":              "resolved",
					"resolved_pnl_cents":        pnlCents,
					"unfilled_refunded_cents":   po.UnfilledRefundedCents + unfilledRefundCents,
					"pool_balance_before_cents": before,
					"pool_balance_after_cents":  poolBalance,
				}).Error; err != nil {
				return err
			}
		}

		// Update liquidity pool balance
		if err := tx.Model(&LiquidityPool{}).Where("id = 1").
			Updates(map[string]any{
				"balance_cents": poolBalance,
				"updated_ts":    nowMillis(),
			}).Error; err != nil {
			return err
		}

		// Recalculate total_pnl_cents from all resolved orders
		if err := tx.Exec(`
UPDATE liquidity_pool SET total_pnl_cents = (
    SELECT COALESCE(SUM(resolved_pnl_cents), 0) FROM orders WHERE is_real = true AND order_status = 'resolved'
)`).Error; err != nil {
			return err
		}

		return nil
	})
}

// ResolveSimulatedOrders resolves all simulated orders for a settled market.
// Assumes full fill at suggested_size. No pool adjustments — simulated money.
// Handles buy_no: NO wins when result="no" (flipped from buy_yes).
func (d *DB) ResolveSimulatedOrders(ctx context.Context, marketTicker, result string) error {
	return d.db.WithContext(ctx).Exec(`
UPDATE orders
SET order_status = 'resolved',
    resolved_pnl_cents = CASE
        WHEN action = 'buy_no' AND ? = 'no' THEN CAST(suggested_size * 100 AS INTEGER) - CAST(suggested_size * market_price * 100 AS INTEGER)
        WHEN action = 'buy_no' AND ? != 'no' THEN -CAST(suggested_size * market_price * 100 AS INTEGER)
        WHEN ? = 'yes' THEN CAST(suggested_size * 100 AS INTEGER) - CAST(suggested_size * market_price * 100 AS INTEGER)
        ELSE -CAST(suggested_size * market_price * 100 AS INTEGER)
    END
WHERE is_real = false
  AND market_ticker = ?
  AND (order_status IS NULL OR order_status NOT IN ('resolved','failed','canceled'))`,
		result, result, result, marketTicker).Error
}

// UnresolvedRealOrder is a real order with a Kalshi order ID that hasn't reached a terminal status.
type UnresolvedRealOrder struct {
	ID            int64
	KalshiOrderID string
	MarketTicker  string
	OrderStatus   string
}

// GetUnresolvedRealOrders returns real orders that have a Kalshi order ID
// but haven't reached a terminal status (resolved or failed).
func (d *DB) GetUnresolvedRealOrders(ctx context.Context) ([]UnresolvedRealOrder, error) {
	var out []UnresolvedRealOrder
	err := d.db.WithContext(ctx).Raw(`
		SELECT id, kalshi_order_id, market_ticker, order_status
		FROM orders
		WHERE is_real = true
		  AND kalshi_order_id IS NOT NULL AND kalshi_order_id != ''
		  AND order_status NOT IN ('resolved', 'failed', 'canceled')`).Scan(&out).Error
	return out, err
}

// UpdateRealOrderStatus updates the status and fill count of a real order
// based on a fresh fetch from Kalshi. On canceled, refunds unfilled portion
// for buy/buy_no orders. Sells never deduct — skip refund for sell cancels.
// reason is appended to Context when status is canceled — explains why.
func (d *DB) UpdateRealOrderStatus(ctx context.Context, orderID int64, fillCount float64, status string, reason string) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o Order
		if err := tx.Where("id = ?", orderID).First(&o).Error; err != nil {
			return err
		}
		if o.OrderStatus == "resolved" || o.OrderStatus == "failed" || o.OrderStatus == "canceled" {
			return nil // idempotent — already terminal
		}
		updates := map[string]any{
			"fill_count":   fillCount,
			"order_status": status,
		}
		if status == "canceled" && reason != "" {
			updates["context"] = appendContextReason(o.Context, reason)
		}
		// Sells never deduct — refunding would inflate the pool.
		if status == "canceled" && o.Action != "sell" {
			refunded, newBalance, err := refundUnfilled(tx, &o, fillCount)
			if err != nil {
				return err
			}
			updates["unfilled_refunded_cents"] = o.UnfilledRefundedCents
			if refunded > 0 {
				updates["pool_balance_before_cents"] = newBalance - refunded
				updates["pool_balance_after_cents"] = newBalance
			}
		}
		return tx.Model(&Order{}).Where("id = ?", orderID).Updates(updates).Error
	})
}

// DropDuplicatePaperOrders removes paper orders (is_real=false) that share the
// same (ts, action, strategy, suggested_size, market_price) tuple. Keeps the
// row with the lowest id (first inserted). Called daily by cron in main.go.
func (d *DB) DropDuplicatePaperOrders(ctx context.Context) (int64, error) {
	res := d.db.WithContext(ctx).Exec(`
DELETE FROM orders
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY ts, action, strategy, suggested_size, market_price
            ORDER BY id
        ) AS rn
        FROM orders
        WHERE is_real = false
    ) WHERE rn > 1
)`)
	return res.RowsAffected, res.Error
}

// refundUnfilled refunds the unfilled portion of an order to the liquidity pool
// within the given transaction. Idempotent: only refunds the delta between
// total unfilled cost and what's already been refunded. Mutates o.UnfilledRefundedCents.
// Returns (refundedCents, newPoolBalance, error). refundedCents=0 means no refund needed.
func refundUnfilled(tx *gorm.DB, o *Order, fillCount float64) (int64, int64, error) {
	totalUnfilledCents := int64((o.SuggestedSize - fillCount) * o.MarketPrice * 100)
	if totalUnfilledCents < 0 {
		totalUnfilledCents = 0
	}
	remainingRefund := totalUnfilledCents - o.UnfilledRefundedCents
	if remainingRefund <= 0 {
		return 0, 0, nil
	}
	var newBalance int64
	err := tx.Raw(`
UPDATE liquidity_pool
SET balance_cents = balance_cents + ?,
    total_spent_cents = GREATEST(total_spent_cents - ?, 0),
    updated_ts = ?
WHERE id = 1
RETURNING balance_cents`,
		remainingRefund, remainingRefund, nowMillis()).Scan(&newBalance).Error
	if err != nil {
		return 0, 0, err
	}
	o.UnfilledRefundedCents += remainingRefund
	return remainingRefund, newBalance, nil
}
