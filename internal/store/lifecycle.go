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
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET status='determined', result=?, settlement_ts=?, settlement_value=?, last_updated_ts=?
WHERE market_ticker=?`,
			le.Result, le.DeterminationTS, le.SettlementValue, now, le.MarketTicker)
		return err

	case "settled":
		// P6: Prune the event if it has zero ticks after settlement.
		// P5: Classify coverage for events that survive.
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET status='finalized', result=?, settlement_ts=?, settlement_value=?, last_updated_ts=?
WHERE market_ticker=?`,
			le.Result, le.SettledTS, le.SettlementValue, now, le.MarketTicker)
		if err != nil {
			return err
		}

		// Get the event_ticker for this market
		var eventTicker string
		err = d.db.QueryRowContext(ctx, "SELECT event_ticker FROM markets WHERE market_ticker = ?", le.MarketTicker).Scan(&eventTicker)
		if err != nil || eventTicker == "" {
			return err
		}

		// Check if the other market is also settled (both sides done)
		var pending int
		err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM markets WHERE event_ticker = ? AND status != 'finalized'", eventTicker).Scan(&pending)
		if err != nil || pending > 0 {
			return nil // other market not settled yet — defer cleanup
		}

		// P5: Classify coverage
		_ = d.SetCoverage(ctx, eventTicker)

		// P6: Prune if zero ticks and zero points
		cov, _ := d.GetCoverage(ctx, eventTicker)
		if cov == "none" {
			// No data — prune immediately via cascade trigger
			_, _ = d.db.ExecContext(ctx, "DELETE FROM events WHERE event_ticker = ?", eventTicker)
			return nil
		}

		// P7: Drop payloads for non-full events
		if cov != "full" && cov != "" {
			_ = d.DropOrphanPayloads(ctx, eventTicker)
		}

		return nil

	case "close_date_updated":
		_, err := d.db.ExecContext(ctx, `
UPDATE markets SET close_ts=?, last_updated_ts=? WHERE market_ticker=?`,
			le.CloseTS, now, le.MarketTicker)
		return err

	default:
		return nil
	}
}
