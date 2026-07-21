# internal/reconciler

Background service that fills settlement gaps by polling Kalshi REST API for unresolved markets.

## Problem

When WS disconnects during market settlement, the `settled` lifecycle event is missed. Market stays `active` in DB, `result` stays empty. Orders on that market show unresolved in PnL analysis. Scanner eventually fixes this (24h), but that's a long gap.

## Flow

1. Poll DB every `reconciler_interval_secs` (default: 300s) for unresolved markets
2. For each: `GET /markets/{ticker}` via REST client
3. If REST has result or different status: upsert market row
4. If market is finalized: run `FinalizeEventIfNeeded` (coverage classification, payload pruning)

## Unresolved Market Criteria

- Has orders but `result` is NULL/empty (missed WS settled event), OR
- Status is `open`/`active` but `close_ts + 30min grace` has elapsed, OR
- Has `result` set AND has real orders in non-terminal status (`ResolveRealOrders` never ran — `determined` set result but `settled` event was missed)

Third clause handled in `GetUnresolvedMarkets` (store/markets.go). Catches the bug class where DB already has the result from `determined` or daily scan, but order resolution never fired.

## FinalizeEventIfNeeded

Extracted from `ApplyLifecycleEvent` "settled" case. Runs post-settlement cleanup:
- Checks both markets in event are finalized
- `SetCoverage` — classifies as full/low_freq/none
- Prunes event if coverage is `none` AND no orders (protects order data from P6 deletion)
- Drops raw payloads for non-`full` coverage

## Files

- `reconciler.go` — Reconciler struct, Run loop, reconcile pass

## Config

- `reconciler_interval_secs` — poll interval (default: 300)

## Gotchas

- Uses same REST client as scanner — shares rate limiter. Won't hammer API.
- Only fetches markets that need reconciliation, not all series. Targeted.
- Grace period (30min) avoids fetching markets that are just slow to settle.
- `FinalizeEventIfNeeded` protects events with orders from P6 pruning — original `ApplyLifecycleEvent` doesn't do this. Bug fix.
- `ResolveRealOrders` / `ResolveSimulatedOrders` run per-market whenever REST returns a result — NOT gated on `finalized[event]` dedup. Tennis events have 2 markets (one per player); gating on event finalization skips the second market. Event-level dedup only applies to `FinalizeEventIfNeeded` (coverage classification, payload pruning).
