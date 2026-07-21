---
name: backfill-orders
description: One-shot backfill of stale real order status and unresolved markets. Use when Fahad reports orders stuck in filled/submitted with no PnL, real trades tab showing wrong status, or markets resolved on Kalshi but not in our DB.
---

# Backfill Orders

One-shot CLI that catches up stale real orders and unresolved markets. Equivalent to running `orderbackfill` + `reconciler` once across the full backlog.

## When to use

- Real orders tab shows `filled`/`submitted` but no `resolved` status or PnL
- Match resolved on Kalshi but our DB still shows order as non-terminal
- After WS reconnect storm — missed `settled` lifecycle events
- After deploy that fixes order resolution logic — catch up the backlog
- Periodic sanity check on stale orders

## Run on mint (prod)

```bash
ssh mint 'cd /home/fahad/kalshi-ghost-trader && APP_ENV=prod go run ./cmd/backfill-orders -dry-run'
```

Dry-run first. Shows what would change without writing. Look for:
- `order status change` — orders that need status update from Kalshi REST
- `market update` — markets that need result/status update
- `resolved=N` — markets where `ResolveRealOrders` will run

If dry-run looks correct, run for real:

```bash
ssh mint 'cd /home/fahad/kalshi-ghost-trader && APP_ENV=prod go run ./cmd/backfill-orders'
```

## Flags

- `-dry-run` — show changes, no writes. **Always run this first.**
- `-orders` — only backfill order status, skip market resolution
- `-markets` — only resolve markets, skip order status backfill
- `-log-level DEBUG|INFO|WARN|ERROR` — log verbosity (default INFO)

`-orders` and `-markets` are mutually exclusive. Default: both.

## What it does

1. **Orders pass** — `GetUnresolvedRealOrders` → fetch each from `GET /portfolio/orders/{id}` → update `order_status` + `fill_count`. Status mapping: `resting`→`submitted`, `canceled`→`canceled`, `executed`→`filled`.
2. **Markets pass** — `GetUnresolvedMarkets` → fetch each from `GET /markets/{ticker}` → upsert market row → if result set, run `ResolveRealOrders` + `ResolveSimulatedOrders` per-market → `FinalizeEventIfNeeded` once per event.

## Unresolved market criteria

A market is picked up if ANY of:
- Has orders but `result` is NULL/empty (missed WS `settled` event)
- Status `open`/`active` but `close_ts + 30min grace` elapsed
- Has result AND has real orders in non-terminal status (`ResolveRealOrders` never ran — e.g. WS `settled` missed, market already had result from `determined` event or daily scan)

Third clause is the catch-all for the bug class where DB has the result but order resolution never fired.

## After running

Restart the backend so the in-memory reconciler picks up any query changes:

```bash
ssh mint 'cd /home/fahad/kalshi-ghost-trader && go build -o ghost-trader . && sudo -n systemctl restart kalshi-ghost-trader'
```

Dashboard refresh should show resolved orders with PnL.

## Gotchas

- Run on mint, not locally — local DB is dev and won't have real orders.
- `APP_ENV=prod` required — otherwise loads `app.dev.yaml` and hits demo Kalshi.
- Uses same REST client as scanner/reconciler — shares rate limiter, won't hammer API.
- `ResolveRealOrders` is idempotent — safe to re-run on already-resolved markets (filtered out by `order_status NOT IN ('resolved','failed','canceled')`).
- Coverage classification + payload pruning runs on `FinalizeEventIfNeeded`. Non-`full` coverage events get payloads NULLed — large tick/orderbook tables make this slow (28s observed on 500k orderbook rows). Normal.
- If dry-run shows 0 updates but orders are still stuck, the bug is elsewhere — check `ApplyLifecycleEvent` "settled" case, WS connection, or order emitter.
