# internal/pnltracker

Spawns per-order goroutines that compute live mark-to-market PnL for filled
real orders every 30 seconds.

## Problem

Real orders have `resolved_pnl_cents` but it's only written at market settlement
by `ResolveRealOrders`. While a position is open, there's no live PnL/ROI.

## Flow

1. Manager goroutine polls DB every 10s for filled buy/buy_no real orders with
   open positions (`GetTrackableRealOrders`)
2. For each new order: spawn a tracker goroutine
3. Tracker goroutine: every 30s, query latest WS tick price for the market,
   compute `unrealized_pnl = (current_price - fill_price) * fill_count * 100`,
   write into `resolved_pnl_cents` + `pnl_updated_ts`
4. Tracker stops when position status transitions to `settled` or `closed`
5. At settlement, `ResolveRealOrders` overwrites `resolved_pnl_cents` with
   final realized PnL

## PnL Computation

- `buy` (long YES): `(yes_price - fill_price) * fill_count * 100`
- `buy_no` (long NO): `(no_price - fill_price) * fill_count * 100`
- `sell`: skipped — realized at fill via `ApplySell`

ROI is computed by the dashboard client-side:
`resolved_pnl_cents / (fill_count * fill_price * 100) * 100`.

## Files

- `tracker.go` — Tracker struct, Run loop, per-order goroutines

## Config

No config. Intervals hardcoded:
- `scanInterval` = 10s (manager poll for new orders)
- `updateInterval` = 30s (per-order PnL update)

## Gotchas

- Reuses `resolved_pnl_cents` for both unrealized (live) and realized (final)
  PnL. `pnl_updated_ts` distinguishes — non-zero while live, overwritten at
  settlement. `ResolveRealOrders` is the final write.
- Only tracks orders with `position_id` set (new position pipeline). Legacy
  orders without positions are skipped.
- Price source is the `ticks` table (latest `ticker` msg per market). If no
  ticks exist yet, skips that cycle silently.
- One goroutine per tracked order. Cleaned up on position close/settle or
  root context cancellation.
