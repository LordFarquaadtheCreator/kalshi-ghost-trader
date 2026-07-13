package store

import "context"

// InsertOrderbookBatch inserts a batch of orderbook events in one transaction.
func (d *DB) InsertOrderbookBatch(ctx context.Context, events []OrderbookEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO orderbook_events (ts, recv_ts, market_ticker, msg_type, sid, seq,
    price, delta, side, payload)
VALUES (?,?,?,?,?,?, ?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.ExecContext(ctx,
			e.TS, e.RecvTS, e.MarketTicker, e.MsgType, e.SID, e.Seq,
			e.Price, e.Delta, e.Side, e.Payload,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
