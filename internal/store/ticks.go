package store

import "context"

// InsertTickBatch inserts a batch of ticks in one transaction.
func (d *DB) InsertTickBatch(ctx context.Context, ticks []Tick) error {
	if len(ticks) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO ticks (ts, recv_ts, market_ticker, msg_type, sid, seq,
    price, yes_bid, yes_ask, yes_bid_size, yes_ask_size, volume, open_interest, dollar_volume,
    dollar_open_interest, last_trade_size, trade_id, no_price, taker_side, taker_outcome_side, taker_book_side,
    payload)
VALUES (?,?,?,?,?,?, ?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range ticks {
		if _, err := stmt.ExecContext(ctx,
			t.TS, t.RecvTS, t.MarketTicker, t.MsgType, t.SID, t.Seq,
			t.Price, t.YesBid, t.YesAsk, t.YesBidSize, t.YesAskSize,
			t.Volume, t.OpenInterest, t.DollarVolume,
			t.DollarOpenInterest, t.LastTradeSize, t.TradeID, t.NoPrice,
			t.TakerSide, t.TakerOutcomeSide, t.TakerBookSide,
			t.Payload,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
