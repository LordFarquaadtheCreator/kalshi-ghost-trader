package store

import "context"

// InsertLifecycleEvent stores a market lifecycle WS event.
func (d *DB) InsertLifecycleEvent(ctx context.Context, le LifecycleEvent) error {
	return d.db.WithContext(ctx).Create(&le).Error
}

// InsertEventLifecycleEvent stores an event_lifecycle WS message.
func (d *DB) InsertEventLifecycleEvent(ctx context.Context, el EventLifecycleEvent) error {
	return d.db.WithContext(ctx).Create(&el).Error
}

// ApplyLifecycleEvent updates the markets table based on a lifecycle event.
// Only explicit WS events are mapped — implicit transitions (initialized->active,
// active->closed) emit no WS event and rely on REST scan for correction.
// Each event type only updates its own columns — preserves close_ts and
// settlement_ts from other sources (REST scan, earlier lifecycle events).
func (d *DB) ApplyLifecycleEvent(ctx context.Context, le LifecycleEvent) error {
	now := nowMillis()

	switch le.EventType {
	case "activated":
		// Conditional: only set open_ts if le.OpenTS != 0, else preserve existing
		return d.db.WithContext(ctx).Exec(`
UPDATE markets SET status='active',
    open_ts=CASE WHEN ?!=0 THEN ? ELSE open_ts END,
    last_updated_ts=?
WHERE market_ticker=?`,
			le.OpenTS, le.OpenTS, now, le.MarketTicker).Error

	case "deactivated":
		return d.db.WithContext(ctx).Model(&Market{}).Where("market_ticker = ?", le.MarketTicker).
			Updates(map[string]any{
				"status":          "inactive",
				"last_updated_ts": now,
			}).Error

	case "determined":
		return d.db.WithContext(ctx).Model(&Market{}).Where("market_ticker = ?", le.MarketTicker).
			Updates(map[string]any{
				"status":           "determined",
				"result":           le.Result,
				"settlement_ts":    le.DeterminationTS,
				"settlement_value": le.SettlementValue,
				"last_updated_ts":  now,
			}).Error

	case "settled":
		// P6: Prune the event if it has zero ticks after settlement.
		// P5: Classify coverage for events that survive.
		err := d.db.WithContext(ctx).Model(&Market{}).Where("market_ticker = ?", le.MarketTicker).
			Updates(map[string]any{
				"status":           "finalized",
				"result":           le.Result,
				"settlement_ts":    le.SettledTS,
				"settlement_value": le.SettlementValue,
				"last_updated_ts":  now,
			}).Error
		if err != nil {
			return err
		}

		// Resolve all orders for this market
		if le.Result != "" {
			_ = d.ResolveRealOrders(ctx, le.MarketTicker, le.Result)
			_ = d.ResolveSimulatedOrders(ctx, le.MarketTicker, le.Result)
		}

		// Get the event_ticker for this market
		var m Market
		err = d.db.WithContext(ctx).Select("event_ticker").Where("market_ticker = ?", le.MarketTicker).First(&m).Error
		if err != nil || m.EventTicker == "" {
			return err
		}

		// Check if the other market is also settled (both sides done)
		var pending int64
		err = d.db.WithContext(ctx).Model(&Market{}).Where("event_ticker = ? AND status != 'finalized'", m.EventTicker).Count(&pending).Error
		if err != nil || pending > 0 {
			return nil
		}

		// P5: Classify coverage
		_ = d.SetCoverage(ctx, m.EventTicker)

		// P6: Prune if zero ticks and zero points
		cov, _ := d.GetCoverage(ctx, m.EventTicker)
		if cov == "none" {
			d.db.WithContext(ctx).Where("event_ticker = ?", m.EventTicker).Delete(&Event{})
			return nil
		}

		// P7: Drop payloads for non-full events
		if cov != "full" && cov != "" {
			_ = d.DropOrphanPayloads(ctx, m.EventTicker)
		}

		return nil

	case "close_date_updated":
		return d.db.WithContext(ctx).Model(&Market{}).Where("market_ticker = ?", le.MarketTicker).
			Updates(map[string]any{
				"close_ts":        le.CloseTS,
				"last_updated_ts": now,
			}).Error

	default:
		return nil
	}
}
