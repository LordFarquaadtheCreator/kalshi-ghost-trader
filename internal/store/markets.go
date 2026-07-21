package store

import (
	"context"

	"gorm.io/gorm/clause"
)

// UpsertMarket inserts or updates a market row.
func (d *DB) UpsertMarket(ctx context.Context, m Market) error {
	_, err := d.UpsertMarketCheckNew(ctx, m)
	return err
}

// UpsertMarketCheckNew inserts or updates a market. Returns true if new.
func (d *DB) UpsertMarketCheckNew(ctx context.Context, m Market) (bool, error) {
	now := nowMillis()
	m.FirstSeenTS = now
	m.LastUpdatedTS = now

	res := d.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&m)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}

	res = d.db.WithContext(ctx).Model(&Market{}).Where("market_ticker = ?", m.MarketTicker).
		Updates(map[string]any{
			"status":           m.Status,
			"occurrence_ts":    m.OccurrenceTS,
			"open_ts":          m.OpenTS,
			"close_ts":         m.CloseTS,
			"result":           m.Result,
			"settlement_ts":    m.SettlementTS,
			"settlement_value": m.SettlementValue,
			"last_updated_ts":  now,
		})
	return false, res.Error
}

// GetActiveMarkets returns markets eligible for tracking: REST status "open"
// or WS lifecycle status "active"/"determined". Kalshi REST uses "open";
// lifecycle WS "activated" maps to "active", "determined" means result known
// but not yet settled. Keeping tracking through "determined" ensures the
// "settled" lifecycle event still arrives and triggers order resolution.
func (d *DB) GetActiveMarkets(ctx context.Context) ([]Market, error) {
	var markets []Market
	err := d.db.WithContext(ctx).
		Where("status IN ?", []string{"open", "active", "determined"}).
		Where("result != ?", "scalar").
		Order("occurrence_ts").Find(&markets).Error
	return markets, err
}

// GetUnresolvedMarkets returns markets that need REST reconciliation:
//   - Has orders but no result (missed WS settled event)
//   - Status active/open but close_ts + grace period elapsed (should have settled)
//   - Has result but orders still in non-terminal status (ResolveRealOrders
//     never ran — e.g. WS settled event missed, market already has result
//     from determined event or daily scan)
//
// Deduplicated by market_ticker. Ordered by close_ts ascending (oldest first).
func (d *DB) GetUnresolvedMarkets(ctx context.Context, graceMS int64) ([]Market, error) {
	now := nowMillis()
	var markets []Market
	err := d.db.WithContext(ctx).Raw(`
SELECT m.market_ticker, m.event_ticker, m.series_ticker, m.player_name, m.tennis_competitor,
    m.status, m.occurrence_ts, m.open_ts, m.close_ts, m.result, m.settlement_ts, m.settlement_value,
    m.first_seen_ts, m.last_updated_ts
FROM markets m
WHERE (
    (m.result IS NULL OR m.result = '')
    AND EXISTS (SELECT 1 FROM orders o WHERE o.market_ticker = m.market_ticker)
)
OR (
    m.status IN ('open', 'active')
    AND m.close_ts > 0
    AND m.close_ts + ? < ?
)
OR (
    m.result IS NOT NULL AND m.result != ''
    AND EXISTS (
        SELECT 1 FROM orders o
        WHERE o.market_ticker = m.market_ticker
          AND o.is_real = true
          AND o.order_status NOT IN ('resolved', 'failed', 'canceled')
    )
)
ORDER BY m.close_ts ASC`, graceMS, now).Scan(&markets).Error
	return markets, err
}

// GetMarket returns a single market by ticker. Returns gorm.ErrRecordNotFound if not found.
func (d *DB) GetMarket(ctx context.Context, marketTicker string) (Market, error) {
	var m Market
	err := d.db.WithContext(ctx).Where("market_ticker = ?", marketTicker).First(&m).Error
	return m, err
}

// GetMarketsByEvent returns all markets for a given event.
func (d *DB) GetMarketsByEvent(ctx context.Context, eventTicker string) ([]Market, error) {
	var markets []Market
	err := d.db.WithContext(ctx).Where("event_ticker = ?", eventTicker).Find(&markets).Error
	return markets, err
}

// GetUpcomingMarkets returns active markets whose occurrence_ts is in the future.
// Used by the schedule checker to refresh stale schedule data from REST.
func (d *DB) GetUpcomingMarkets(ctx context.Context) ([]Market, error) {
	now := nowMillis()
	var markets []Market
	err := d.db.WithContext(ctx).
		Where("status IN ?", []string{"open", "active"}).
		Where("result != ?", "scalar").
		Where("occurrence_ts > ?", now).
		Order("occurrence_ts").Find(&markets).Error
	return markets, err
}

// GetMarketsClosingWithin returns active markets whose close_ts falls within
// [now, now+withinSecs]. Used by the close-timer strategy to find markets
// approaching their close window.
func (d *DB) GetMarketsClosingWithin(ctx context.Context, withinSecs int64) ([]Market, error) {
	now := nowMillis()
	cutoff := now + withinSecs*1000
	var markets []Market
	err := d.db.WithContext(ctx).
		Where("status IN ?", []string{"open", "active"}).
		Where("result != ?", "scalar").
		Where("close_ts > 0 AND close_ts BETWEEN ? AND ?", now, cutoff).
		Order("close_ts").Find(&markets).Error
	return markets, err
}
