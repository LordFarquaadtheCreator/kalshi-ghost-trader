package store

import (
	"context"
	"log/slog"
)

// PositionSettler settles open positions when a market settles via WS.
// Implemented by positions.Manager (wired in main.go via DB.SetPositionSettler).
// Decouples store from positions package (avoids import cycle).
type PositionSettler interface {
	Settle(ctx context.Context, matchTicker, marketTicker, strategy string, isReal bool, won bool) (positionID int64, settlementPNLCents int64, remainingContracts float64, err error)
}

// positionSettlerHook is set by main.go to enable WS-path position settlement.
// nil = no position settlement (legacy behavior, backward compat).
var positionSettlerHook PositionSettler

// SetPositionSettler wires the position settler for WS lifecycle events.
// Called once at startup. Pass nil to disable.
func SetPositionSettler(p PositionSettler) { positionSettlerHook = p }

// settlePositionsForMarket settles all open positions for a market at result.
// Called from ApplyLifecycleEvent "settled" case. No-op if no hook set or
// no open positions. Errors logged, not propagated — settlement is best-effort.
func (d *DB) settlePositionsForMarket(ctx context.Context, marketTicker, eventTicker, result string, log *slog.Logger) {
	if positionSettlerHook == nil {
		return
	}
	won := result == "yes"
	var openPositions []Position
	if err := d.db.WithContext(ctx).
		Where("market_ticker = ? AND status = ?", marketTicker, PositionStatusOpen).
		Find(&openPositions).Error; err != nil {
		if log != nil {
			log.Warn("lifecycle: fetch open positions failed", "market", marketTicker, "err", err)
		}
		return
	}
	for _, p := range openPositions {
		_, settlePnL, remaining, err := positionSettlerHook.Settle(
			ctx, p.MatchTicker, p.MarketTicker, p.Strategy, p.IsReal, won)
		if err != nil {
			if log != nil {
				log.Warn("lifecycle: settle position failed",
					"position_id", p.ID, "market", p.MarketTicker, "err", err)
			}
			continue
		}
		if log != nil && remaining > 0 {
			log.Info("lifecycle: settled position",
				"position_id", p.ID, "market", p.MarketTicker,
				"strategy", p.Strategy, "is_real", p.IsReal,
				"result", result, "remaining_contracts", remaining,
				"settlement_pnl_cents", settlePnL)
		}
	}
}

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
		// Conditional: only set open_ts if le.OpenTS != 0, else preserve existing.
		// Cast the comparison to bigint so pgx binds the int64 param as int8,
		// not int4 (the literal 0 defaults to int4 and would force overflow on
		// ms-epoch values > 2^31-1).
		return d.db.WithContext(ctx).Exec(`
UPDATE markets SET status='active',
    open_ts=CASE WHEN ?::bigint!=0 THEN ? ELSE open_ts END,
    is_deactivated=false,
    last_updated_ts=?
WHERE market_ticker=?`,
			le.OpenTS, le.OpenTS, now, le.MarketTicker).Error

	case "deactivated":
		return d.db.WithContext(ctx).Model(&Market{}).Where("market_ticker = ?", le.MarketTicker).
			Updates(map[string]any{
				"status":          "inactive",
				"is_deactivated":  true,
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
			// Settle any open positions for this market (sell-to-close pipeline).
			// No-op if no position rows exist (legacy orders).
			d.settlePositionsForMarket(ctx, le.MarketTicker, "", le.Result, d.log)
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
