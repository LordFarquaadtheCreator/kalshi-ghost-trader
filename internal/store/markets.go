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
		marketSelectColumns+` WHERE status IN ('open', 'active') AND result != 'scalar' ORDER BY occurrence_ts`)
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

// GetUnresolvedMarkets returns markets that need REST reconciliation:
//   - Has orders but no result (missed WS settled event)
//   - Status active/open but close_ts + grace period elapsed (should have settled)
//
// Deduplicated by market_ticker. Ordered by close_ts ascending (oldest first).
func (d *DB) GetUnresolvedMarkets(ctx context.Context, graceMS int64) ([]Market, error) {
	now := nowMillis()
	rows, err := d.db.QueryContext(ctx, `
SELECT m.market_ticker, m.event_ticker, m.series_ticker, m.player_name, m.tennis_competitor,
    m.status, m.occurrence_ts, m.open_ts, m.close_ts, m.result, m.settlement_ts, m.settlement_value
FROM markets m
WHERE (
    -- Has orders but no result
    (m.result IS NULL OR m.result = '')
    AND EXISTS (SELECT 1 FROM orders o WHERE o.market_ticker = m.market_ticker)
)
OR (
    -- Active/open past close_ts + grace
    m.status IN ('open', 'active')
    AND m.close_ts > 0
    AND m.close_ts + ? < ?
)
ORDER BY m.close_ts ASC`, graceMS, now)
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

// GetUpcomingMarkets returns active markets whose occurrence_ts is in the future.
// Used by the schedule checker to refresh stale schedule data from REST.
func (d *DB) GetUpcomingMarkets(ctx context.Context) ([]Market, error) {
	now := nowMillis()
	rows, err := d.db.QueryContext(ctx,
		marketSelectColumns+` WHERE status IN ('open', 'active') AND result != 'scalar' AND occurrence_ts > ? ORDER BY occurrence_ts`,
		now)
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

// GetMarketsClosingWithin returns active markets whose close_ts falls within
// [now, now+withinSecs]. Used by the close-timer strategy to find markets
// approaching their close window.
func (d *DB) GetMarketsClosingWithin(ctx context.Context, withinSecs int64) ([]Market, error) {
	now := nowMillis()
	cutoff := now + withinSecs*1000
	rows, err := d.db.QueryContext(ctx,
		marketSelectColumns+` WHERE status IN ('open','active') AND result != 'scalar' AND close_ts > 0 AND close_ts BETWEEN ? AND ? ORDER BY close_ts`,
		now, cutoff)
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
