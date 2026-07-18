# internal/orderbackfill

Background service that polls Kalshi REST API for real orders stuck in non-terminal status.

## Problem

Real orders submitted via `KalshiOrderEmitter` get an immediate response with `order_id`, `fill_count`, and `status`. But if the WS user orders channel disconnects or misses an update, the order status in our DB never progresses — stuck in `submitted` or `partial` forever. Market settlement via `ResolveRealOrders` only runs when the reconciler finalizes the market, not the order itself.

## Flow

1. Poll DB every `order_backfill_interval_secs` (default: 120s) for unresolved real orders
2. For each: `GET /portfolio/orders/{order_id}` via REST client
3. Map Kalshi status (`resting`→`submitted`, `canceled`→`canceled`, `executed`→`filled`)
4. Update DB with latest status + fill count if changed

## Terminal Statuses

Orders are considered resolved and excluded from backfill when `order_status` is:
- `resolved` — market settled, P&L computed by `ResolveRealOrders`
- `failed` — order submission failed, marked by `MarkRealOrderFailed`
- `canceled` — order canceled on Kalshi

## Files

- `backfill.go` — Backfill struct, Run loop, backfill pass

## Config

- `order_backfill_interval_secs` — poll interval (default: 120)

## Gotchas

- Uses same REST client as scanner/reconciler — shares rate limiter
- Only fetches orders with a non-empty `kalshi_order_id` — paper orders skipped
- Does NOT resolve P&L — that's the reconciler's job via `ResolveRealOrders` after market settlement
- Status mapping: Kalshi uses `resting`/`canceled`/`executed`; we use `submitted`/`canceled`/`filled`
