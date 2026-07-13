package store

import (
	"context"
	"database/sql"
)

// marketSelectColumns is the shared column list for market queries.
const marketSelectColumns = `
SELECT market_ticker, event_ticker, series_ticker, player_name, tennis_competitor,
    status, occurrence_ts, open_ts, close_ts, result, settlement_ts, settlement_value
FROM markets`

// scanMarket scans a market row from a *sql.Rows into a Market.
// Nullable columns use sql.Null* types.
func scanMarket(rows *sql.Rows) (Market, error) {
	var m Market
	var occurrenceTS, openTS, closeTS, settlementTS sql.NullInt64
	var tennisCompetitor, result, settlementValue sql.NullString
	if err := rows.Scan(
		&m.MarketTicker, &m.EventTicker, &m.SeriesTicker, &m.PlayerName, &tennisCompetitor,
		&m.Status, &occurrenceTS, &openTS, &closeTS, &result, &settlementTS, &settlementValue,
	); err != nil {
		return m, err
	}
	m.TennisCompetitor = tennisCompetitor.String
	m.OccurrenceTS = occurrenceTS.Int64
	m.OpenTS = openTS.Int64
	m.CloseTS = closeTS.Int64
	m.Result = result.String
	m.SettlementTS = settlementTS.Int64
	m.SettlementValue = settlementValue.String
	return m, nil
}

// UpsertMarket inserts or updates a market row.
func (d *DB) UpsertMarket(ctx context.Context, m Market) error {
	_, err := d.UpsertMarketCheckNew(ctx, m)
	return err
}

// UpsertMarketCheckNew inserts or updates a market. Returns true if new.
func (d *DB) UpsertMarketCheckNew(ctx context.Context, m Market) (bool, error) {
	now := nowMillis()
	res, err := d.db.ExecContext(ctx, `
INSERT OR IGNORE INTO markets (market_ticker, event_ticker, series_ticker, player_name, tennis_competitor,
    status, occurrence_ts, open_ts, close_ts, result, settlement_ts, settlement_value,
    first_seen_ts, last_updated_ts)
VALUES (?,?,?,?,?,?, ?,?,?,?,?,?, ?,?)`,
		m.MarketTicker, m.EventTicker, m.SeriesTicker, m.PlayerName, m.TennisCompetitor,
		m.Status, m.OccurrenceTS, m.OpenTS, m.CloseTS, m.Result, m.SettlementTS, m.SettlementValue,
		now, now,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return true, nil
	}
	_, err = d.db.ExecContext(ctx, `
UPDATE markets SET status=?, occurrence_ts=?, open_ts=?, close_ts=?,
    result=?, settlement_ts=?, settlement_value=?, last_updated_ts=?
WHERE market_ticker=?`,
		m.Status, m.OccurrenceTS, m.OpenTS, m.CloseTS,
		m.Result, m.SettlementTS, m.SettlementValue, now, m.MarketTicker,
	)
	return false, err
}

// GetActiveMarkets returns markets eligible for tracking: REST status "open"
// or WS lifecycle status "active". Kalshi REST uses "open"; lifecycle WS
// "activated" event maps to "active". Both mean market is live.
func (d *DB) GetActiveMarkets(ctx context.Context) ([]Market, error) {
	rows, err := d.db.QueryContext(ctx,
		marketSelectColumns+` WHERE status IN ('open', 'active') ORDER BY occurrence_ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var markets []Market
	for rows.Next() {
		m, err := scanMarket(rows)
		if err != nil {
			return nil, err
		}
		markets = append(markets, m)
	}
	return markets, rows.Err()
}

// GetMarketsByEvent returns all markets for a given event.
func (d *DB) GetMarketsByEvent(ctx context.Context, eventTicker string) ([]Market, error) {
	rows, err := d.db.QueryContext(ctx,
		marketSelectColumns+` WHERE event_ticker = ?`, eventTicker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var markets []Market
	for rows.Next() {
		m, err := scanMarket(rows)
		if err != nil {
			return nil, err
		}
		markets = append(markets, m)
	}
	return markets, rows.Err()
}
