package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// tickCopyColumns lists the ticks table columns in the same order as
// tickCopyRow produces values. The auto-increment `id` column is excluded —
// Postgres assigns it during COPY.
var tickCopyColumns = []string{
	"ts", "recv_ts", "market_ticker", "msg_type", "sid", "seq",
	"price", "yes_bid", "yes_ask", "yes_bid_size", "yes_ask_size",
	"volume", "open_interest", "dollar_volume", "dollar_open_interest",
	"last_trade_size", "trade_id", "no_price",
	"taker_side", "taker_outcome_side", "taker_book_side", "payload",
}

// orderbookCopyColumns lists the orderbook_events table columns in COPY order.
var orderbookCopyColumns = []string{
	"ts", "recv_ts", "market_ticker", "msg_type", "sid", "seq",
	"price", "delta", "side", "payload",
}

// withPgxConn borrows a *pgx.Conn from the GORM/sql.DB pool for the duration
// of fn. The connection is returned to the pool when fn returns. This reaches
// through database/sql → pgx stdlib to access the native pgx CopyFrom API
// without opening a separate connection pool.
func (d *DB) withPgxConn(ctx context.Context, fn func(*pgx.Conn) error) error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		pgc, ok := dc.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("underlying conn is %T, not *stdlib.Conn", dc)
		}
		return fn(pgc.Conn())
	})
}

// CopyFromTicks inserts ticks via PostgreSQL COPY. Replaces CreateInBatches
// on the hot path — COPY avoids per-row parse/plan overhead.
func (d *DB) CopyFromTicks(ctx context.Context, ticks []Tick) error {
	if len(ticks) == 0 {
		return nil
	}
	rows := make([][]any, len(ticks))
	for i, t := range ticks {
		rows[i] = []any{
			t.TS, t.RecvTS, t.MarketTicker, t.MsgType, t.SID, t.Seq,
			t.Price, t.YesBid, t.YesAsk, t.YesBidSize, t.YesAskSize,
			t.Volume, t.OpenInterest, t.DollarVolume, t.DollarOpenInterest,
			t.LastTradeSize, t.TradeID, t.NoPrice,
			t.TakerSide, t.TakerOutcomeSide, t.TakerBookSide, t.Payload,
		}
	}
	return d.withPgxConn(ctx, func(conn *pgx.Conn) error {
		_, err := conn.CopyFrom(ctx, pgx.Identifier{"ticks"}, tickCopyColumns, pgx.CopyFromRows(rows))
		return err
	})
}

// CopyFromOrderbook inserts orderbook events via PostgreSQL COPY.
func (d *DB) CopyFromOrderbook(ctx context.Context, events []OrderbookEvent) error {
	if len(events) == 0 {
		return nil
	}
	rows := make([][]any, len(events))
	for i, e := range events {
		rows[i] = []any{
			e.TS, e.RecvTS, e.MarketTicker, e.MsgType, e.SID, e.Seq,
			e.Price, e.Delta, e.Side, e.Payload,
		}
	}
	return d.withPgxConn(ctx, func(conn *pgx.Conn) error {
		_, err := conn.CopyFrom(ctx, pgx.Identifier{"orderbook_events"}, orderbookCopyColumns, pgx.CopyFromRows(rows))
		return err
	})
}
