package store

import "context"

// InsertLifecycleEvent stores a market lifecycle WS event.
func (d *DB) InsertLifecycleEvent(ctx context.Context, le LifecycleEvent) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO lifecycle_events (ts, recv_ts, market_ticker, event_type, result,
    open_ts, close_ts, determination_ts, settled_ts, settlement_value, payload)
VALUES (?,?,?,?, ?,?,?,?,?,?,?)`,
		le.TS, le.RecvTS, le.MarketTicker, le.EventType, le.Result,
		le.OpenTS, le.CloseTS, le.DeterminationTS, le.SettledTS, le.SettlementValue, le.Payload,
	)
	return err
}

// InsertEventLifecycleEvent stores an event_lifecycle WS message.
func (d *DB) InsertEventLifecycleEvent(ctx context.Context, el EventLifecycleEvent) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO event_lifecycle_events (ts, recv_ts, event_ticker, series_ticker, title, subtitle, payload)
VALUES (?,?,?,?,?,?,?)`,
		el.TS, el.RecvTS, el.EventTicker, el.SeriesTicker, el.Title, el.Subtitle, el.Payload,
	)
	return err
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
		// status -> active; update open_ts if present; preserve close_ts/settlement_ts
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET status='active',
    open_ts=CASE WHEN ?!=0 THEN ? ELSE open_ts END,
    last_updated_ts=?
WHERE market_ticker=?`,
			le.OpenTS, le.OpenTS, now, le.MarketTicker)
		return err

	case "deactivated":
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET status='inactive', last_updated_ts=?
WHERE market_ticker=?`,
			now, le.MarketTicker)
		return err

	case "determined":
		// status -> determined; update result + settlement_ts + settlement_value; preserve close_ts
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET status='determined', result=?, settlement_ts=?, settlement_value=?, last_updated_ts=?
WHERE market_ticker=?`,
			le.Result, le.DeterminationTS, le.SettlementValue, now, le.MarketTicker)
		return err

	case "settled":
		// status -> finalized; update result + settlement_ts + settlement_value; preserve close_ts
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET status='finalized', result=?, settlement_ts=?, settlement_value=?, last_updated_ts=?
WHERE market_ticker=?`,
			le.Result, le.SettledTS, le.SettlementValue, now, le.MarketTicker)
		return err

	case "close_date_updated":
		// Only update close_ts, keep existing status
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET close_ts=?, last_updated_ts=? WHERE market_ticker=?`,
			le.CloseTS, now, le.MarketTicker)
		return err

	default:
		// created, price_level_structure_updated, metadata_updated — no status change
		return nil
	}
}
