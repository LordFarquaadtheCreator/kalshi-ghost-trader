# /orders — Paper Orders

Simulated paper trading orders split into pending (open positions) and settled trades.

## Files

- `+page.svelte` — No `+page.js`. Client-side polling via `createPoll(() => api.getOrders(), 5000)`.

## Data

- `api.getOrders()` → `GET :6060/api/orders` → `{orders: PaperOrder[], summary: PaperOrderSummary}`
- `PaperOrder`: `{ts, match_ticker, market_ticker, player_name, context, market_price, edge_cents, suggested_size, strategy, result, won, pnl}`
- `result` empty = pending, "yes" = won, else lost.

## State

- `filterStrategy` — filter by strategy name
- `filterResult` — filter by result: won, lost, pending, or all
- `filteredOrders` — `$derived` from filters applied to `data.orders`
- `settledOrders` — `$derived` from `filteredOrders.filter(o => o.result)`
- `pendingOrders` — `$derived` from `filteredOrders.filter(o => !o.result)`
- `filteredSummary` — `$derived` recomputes summary stats from `filteredOrders` (not from API summary)

## UI

- Summary bar: uses `filteredSummary` (reflects active filters)
- Filters: strategy dropdown + result dropdown + count display
- Open Positions table: `CollapsibleSection`, pending orders only, no P&L column
- Settled Trades table: `CollapsibleSection`, resolved orders with WON/LOST badge + P&L
