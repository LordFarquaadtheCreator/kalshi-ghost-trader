package store

import (
	"context"
	"database/sql"
)

// GetTicksByMarket returns all ticks for a market, ordered by ts.
// Used by replay tests to reconstruct historical price data.
func (d *DB) GetTicksByMarket(ctx context.Context, marketTicker string) ([]Tick, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT ts, recv_ts, market_ticker, msg_type, sid, seq,
    price, yes_bid, yes_ask, yes_bid_size, yes_ask_size,
    volume, open_interest, dollar_volume, dollar_open_interest,
    last_trade_size, trade_id, no_price, taker_side, taker_outcome_side, taker_book_side,
    payload
FROM ticks WHERE market_ticker = ? ORDER BY ts`, marketTicker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ticks []Tick
	for rows.Next() {
		var t Tick
		var sid, seq, dollarVolume, dollarOpenInterest sql.NullInt64
		var price, yesBid, yesAsk, yesBidSize, yesAskSize, volume, openInterest,
			lastTradeSize, noPrice sql.NullFloat64
		var tradeID, takerSide, takerOutcomeSide, takerBookSide sql.NullString
		var payload string
		if err := rows.Scan(&t.TS, &t.RecvTS, &t.MarketTicker, &t.MsgType,
			&sid, &seq, &price, &yesBid, &yesAsk, &yesBidSize, &yesAskSize,
			&volume, &openInterest, &dollarVolume, &dollarOpenInterest,
			&lastTradeSize, &tradeID, &noPrice, &takerSide, &takerOutcomeSide, &takerBookSide,
			&payload); err != nil {
			return nil, err
		}
		t.SID = sid.Int64
		t.Seq = seq.Int64
		t.Price = price.Float64
		t.YesBid = yesBid.Float64
		t.YesAsk = yesAsk.Float64
		t.YesBidSize = yesBidSize.Float64
		t.YesAskSize = yesAskSize.Float64
		t.Volume = volume.Float64
		t.OpenInterest = openInterest.Float64
		t.DollarVolume = dollarVolume.Int64
		t.DollarOpenInterest = dollarOpenInterest.Int64
		t.LastTradeSize = lastTradeSize.Float64
		t.TradeID = tradeID.String
		t.NoPrice = noPrice.Float64
		t.TakerSide = takerSide.String
		t.TakerOutcomeSide = takerOutcomeSide.String
		t.TakerBookSide = takerBookSide.String
		t.Payload = payload
		ticks = append(ticks, t)
	}
	return ticks, rows.Err()
}

// GetLatestDollarVolume returns the most recent dollar_volume for a market.
// Used by volratio strategy in live mode.
func (d *DB) GetLatestDollarVolume(ctx context.Context, marketTicker string) (float64, error) {
	var vol sql.NullFloat64
	err := d.db.QueryRowContext(ctx,
		`SELECT dollar_volume FROM ticks
		 WHERE market_ticker = ? AND dollar_volume IS NOT NULL AND dollar_volume > 0
		 ORDER BY ts DESC LIMIT 1`, marketTicker).Scan(&vol)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return vol.Float64, err
}
