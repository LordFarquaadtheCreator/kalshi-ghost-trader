package store

import (
	"context"
	"database/sql"
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
    conv_prob, market_price, edge_cents, suggested_size, set_number, strategy, payload)
VALUES (?,?,?,?,?, ?,?,?,?, ?,?,?)`)
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
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetOrders returns all simulated orders, ordered by timestamp.
func (d *DB) GetOrders(ctx context.Context) ([]Order, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT ts, match_ticker, market_ticker, action, context,
		       conv_prob, market_price, edge_cents, suggested_size, set_number, strategy, payload
		FROM orders ORDER BY ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		var payload sql.NullString
		if err := rows.Scan(&o.TS, &o.MatchTicker, &o.MarketTicker, &o.Action, &o.Context,
			&o.ConvProb, &o.MarketPrice, &o.EdgeCents, &o.SuggestedSize, &o.SetNumber, &o.Strategy, &payload); err != nil {
			return nil, err
		}
		o.Payload = payload.String
		orders = append(orders, o)
	}
	return orders, rows.Err()
}
