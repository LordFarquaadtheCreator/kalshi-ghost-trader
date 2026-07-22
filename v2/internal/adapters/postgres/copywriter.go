package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
)

// TickRow is a flat tick record for COPY insertion into ticks_v2.
type TickRow struct {
	MarketTicker  string
	TS            int64
	PriceCents    int
	YesBidCents   *int
	YesAskCents   *int
	Volume        *int
	Raw           []byte // nullable jsonb
	BestBidCents  *int   // top-of-book depth (A.2.3)
	BestAskCents  *int
	BestBidSize   *int
	BestAskSize   *int
}

// OrderbookRow is a flat orderbook record for COPY insertion into orderbook_v2.
type OrderbookRow struct {
	MarketTicker string
	TS           int64
	IsSnapshot   bool
	PriceCents   *int
	Delta        *int
	Side         *string
	Raw          []byte
}

// CopyWriter uses pgx CopyFrom for bulk insertion into partitioned hot tables.
type CopyWriter struct {
	db *gorm.DB
}

// NewCopyWriter creates a COPY-based writer.
func NewCopyWriter(db *gorm.DB) *CopyWriter {
	return &CopyWriter{db: db}
}

// CopyTicks bulk-inserts tick rows into ticks_v2 via pgx CopyFrom.
func (w *CopyWriter) CopyTicks(ctx context.Context, rows []TickRow) error {
	if len(rows) == 0 {
		return nil
	}
	return w.copyFrom(ctx, pgx.Identifier{"ticks_v2"},
		[]string{"market_ticker", "ts", "price_cents", "yes_bid_cents", "yes_ask_cents", "volume", "raw",
			"best_bid_cents", "best_ask_cents", "best_bid_size", "best_ask_size"},
		len(rows), func(i int) []any {
			r := rows[i]
			return []any{r.MarketTicker, r.TS, r.PriceCents, r.YesBidCents, r.YesAskCents, r.Volume, r.Raw,
				r.BestBidCents, r.BestAskCents, r.BestBidSize, r.BestAskSize}
		})
}

// CopyOrderbook bulk-inserts orderbook rows into orderbook_v2 via pgx CopyFrom.
func (w *CopyWriter) CopyOrderbook(ctx context.Context, rows []OrderbookRow) error {
	if len(rows) == 0 {
		return nil
	}
	return w.copyFrom(ctx, pgx.Identifier{"orderbook_v2"},
		[]string{"market_ticker", "ts", "is_snapshot", "price_cents", "delta", "side", "raw"},
		len(rows), func(i int) []any {
			r := rows[i]
			return []any{r.MarketTicker, r.TS, r.IsSnapshot, r.PriceCents, r.Delta, r.Side, r.Raw}
		})
}

func (w *CopyWriter) copyFrom(ctx context.Context, table pgx.Identifier, columns []string, n int, rowFn func(int) []any) error {
	sqlDB, err := w.db.DB()
	if err != nil {
		return fmt.Errorf("copyfrom: get sqlDB: %w", err)
	}

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("copyfrom: acquire conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	return conn.Raw(func(dc any) error {
		pgConn := dc.(*stdlib.Conn).Conn()
		src := pgx.CopyFromSlice(n, func(i int) ([]any, error) {
			return rowFn(i), nil
		})
		_, err := pgConn.CopyFrom(ctx, table, columns, src)
		return err
	})
}
