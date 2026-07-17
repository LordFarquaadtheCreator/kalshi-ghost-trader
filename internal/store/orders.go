package store

import (
	"context"
	"database/sql"
	"fmt"
)

// InsertOrdersBatch inserts a batch of orders in one transaction.
func (d *DB) InsertOrdersBatch(ctx context.Context, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO orders (ts, match_ticker, market_ticker, action, context,
    conv_prob, market_price, edge_cents, suggested_size, set_number, strategy, payload,
    bankroll, kelly_fraction)
VALUES (?,?,?,?,?, ?,?,?,?, ?,?, ?, ?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, o := range orders {
		var payload interface{}
		if o.Payload != "" {
			payload = o.Payload
		}
		if _, err := stmt.ExecContext(ctx,
			o.TS, o.MatchTicker, o.MarketTicker, o.Action, o.Context,
			o.ConvProb, o.MarketPrice, o.EdgeCents, o.SuggestedSize, o.SetNumber, o.Strategy, payload,
			o.Bankroll, o.KellyFraction,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetOrders returns all simulated orders, ordered by timestamp.
func (d *DB) GetOrders(ctx context.Context) ([]Order, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, ts, match_ticker, market_ticker, action, context,
		       conv_prob, market_price, edge_cents, suggested_size, set_number, strategy, payload,
		       bankroll, kelly_fraction, is_real, kalshi_order_id, fill_count, order_status,
		       resolved_pnl_cents, pool_balance_before_cents, pool_balance_after_cents
		FROM orders ORDER BY ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		var payload sql.NullString
		var kalshiOrderID sql.NullString
		var fillCount sql.NullFloat64
		var orderStatus sql.NullString
		var resolvedPnl, poolBefore, poolAfter sql.NullInt64
		if err := rows.Scan(&o.ID, &o.TS, &o.MatchTicker, &o.MarketTicker, &o.Action, &o.Context,
			&o.ConvProb, &o.MarketPrice, &o.EdgeCents, &o.SuggestedSize, &o.SetNumber, &o.Strategy, &payload,
			&o.Bankroll, &o.KellyFraction, &o.IsReal, &kalshiOrderID, &fillCount, &orderStatus,
			&resolvedPnl, &poolBefore, &poolAfter); err != nil {
			return nil, err
		}
		o.Payload = payload.String
		o.KalshiOrderID = kalshiOrderID.String
		o.FillCount = fillCount.Float64
		o.OrderStatus = orderStatus.String
		o.ResolvedPNLCents = resolvedPnl.Int64
		o.PoolBalanceBeforeCents = poolBefore.Int64
		o.PoolBalanceAfterCents = poolAfter.Int64
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// UpdateRealOrder updates an existing order row with Kalshi response fields.
// Called by KalshiOrderEmitter after order submission.
func (d *DB) UpdateRealOrder(ctx context.Context, orderID int64, kalshiOrderID string, fillCount float64, status string) error {
	_, err := d.db.ExecContext(ctx, `
UPDATE orders SET is_real = 1, kalshi_order_id = ?, fill_count = ?, order_status = ?
WHERE id = ?`,
		kalshiOrderID, fillCount, status, orderID)
	return err
}

// MarkRealOrderFailed marks an order as a failed real submission.
func (d *DB) MarkRealOrderFailed(ctx context.Context, orderID int64) error {
	_, err := d.db.ExecContext(ctx, `
UPDATE orders SET is_real = 1, order_status = 'failed'
WHERE id = ?`, orderID)
	return err
}

// GetRealOrders returns all real orders (is_real=1), ordered by timestamp.
func (d *DB) GetRealOrders(ctx context.Context) ([]Order, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, ts, match_ticker, market_ticker, action, context,
		       conv_prob, market_price, edge_cents, suggested_size, set_number, strategy, payload,
		       bankroll, kelly_fraction, is_real, kalshi_order_id, fill_count, order_status,
		       resolved_pnl_cents, pool_balance_before_cents, pool_balance_after_cents
		FROM orders WHERE is_real = 1 ORDER BY ts DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		var payload sql.NullString
		var kalshiOrderID sql.NullString
		var fillCount sql.NullFloat64
		var orderStatus sql.NullString
		var resolvedPnl, poolBefore, poolAfter sql.NullInt64
		if err := rows.Scan(&o.ID, &o.TS, &o.MatchTicker, &o.MarketTicker, &o.Action, &o.Context,
			&o.ConvProb, &o.MarketPrice, &o.EdgeCents, &o.SuggestedSize, &o.SetNumber, &o.Strategy, &payload,
			&o.Bankroll, &o.KellyFraction, &o.IsReal, &kalshiOrderID, &fillCount, &orderStatus,
			&resolvedPnl, &poolBefore, &poolAfter); err != nil {
			return nil, err
		}
		o.Payload = payload.String
		o.KalshiOrderID = kalshiOrderID.String
		o.FillCount = fillCount.Float64
		o.OrderStatus = orderStatus.String
		o.ResolvedPNLCents = resolvedPnl.Int64
		o.PoolBalanceBeforeCents = poolBefore.Int64
		o.PoolBalanceAfterCents = poolAfter.Int64
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// ResolveRealOrders resolves all real orders for a settled market.
// P&L uses fill_count (not SuggestedSize) — IOC orders may partially fill.
// Updates liquidity pool with realized P&L.
func (d *DB) ResolveRealOrders(ctx context.Context, marketTicker, result string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Fetch unresolved real orders for this market
	rows, err := tx.QueryContext(ctx, `
		SELECT id, fill_count, market_price, order_status
		FROM orders
		WHERE is_real = 1 AND market_ticker = ?
		  AND order_status IN ('submitted', 'filled', 'partial')`, marketTicker)
	if err != nil {
		return err
	}

	type pendingOrder struct {
		id        int64
		fillCount float64
		price     float64
	}
	var pending []pendingOrder
	for rows.Next() {
		var po pendingOrder
		var fc sql.NullFloat64
		if err := rows.Scan(&po.id, &fc, &po.price, new(sql.NullString)); err != nil {
			rows.Close()
			return err
		}
		po.fillCount = fc.Float64
		pending = append(pending, po)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if len(pending) == 0 {
		return tx.Commit()
	}

	// Get current pool balance
	var poolBalance int64
	err = tx.QueryRowContext(ctx, "SELECT balance_cents FROM liquidity_pool WHERE id = 1").Scan(&poolBalance)
	if err != nil {
		return fmt.Errorf("resolve: read liquidity pool: %w", err)
	}

	won := result == "yes"
	for _, po := range pending {
		var pnlCents int64
		if po.fillCount > 0 {
			if won {
				pnlCents = int64(po.fillCount * (1.0 - po.price) * 100)
			} else {
				pnlCents = int64(-po.fillCount * po.price * 100)
			}
		}

		before := poolBalance
		poolBalance += pnlCents

		if _, err := tx.ExecContext(ctx, `
UPDATE orders SET order_status = 'resolved', resolved_pnl_cents = ?,
                  pool_balance_before_cents = ?, pool_balance_after_cents = ?
WHERE id = ?`,
			pnlCents, before, poolBalance, po.id); err != nil {
			return err
		}
	}

	// Update liquidity pool
	if _, err := tx.ExecContext(ctx, `
UPDATE liquidity_pool SET balance_cents = ?, total_pnl_cents = total_pnl_cents + ?,
                           updated_ts = ?
WHERE id = 1`,
		poolBalance, 0, nowMillis()); err != nil {
		return err
	}

	// Recalculate total_pnl_cents from all resolved orders
	if _, err := tx.ExecContext(ctx, `
UPDATE liquidity_pool SET total_pnl_cents = (
    SELECT COALESCE(SUM(resolved_pnl_cents), 0) FROM orders WHERE is_real = 1 AND order_status = 'resolved'
)`); err != nil {
		return err
	}

	return tx.Commit()
}
