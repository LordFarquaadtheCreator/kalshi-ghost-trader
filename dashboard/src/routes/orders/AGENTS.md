# /orders — Paper Orders

Paper trading orders with server-side filtering, cursor pagination, and pre-computed analysis.

## Files

- `+page.js` — SSR disabled. Initial load: `/api/paper-orders/meta` + `/api/paper-orders/summary` + `/api/paper-orders?limit=100` + `/api/paper-orders-insights` in parallel.
- `+page.svelte` — Server-side filtered tables with "Load More" pagination + delta polling. Insights from `+page.js` load + manual refresh + 5-min auto-refresh.

## Data

- `api.getPaperOrdersMeta()` → `GET :6060/api/paper-orders/meta` → `{strategies: string[]}` (60s cache)
- `api.getPaperOrdersSummary(filters)` → `GET :6060/api/paper-orders/summary?strategies=&min_price=&max_price=&match=&result=` → `{strategies: [...], total: {total_orders, resolved, wins, losses, pending, total_invested, net_pnl}}`
- `api.getPaperOrdersPage(filters)` → `GET :6060/api/paper-orders?strategies=&...&cursor_ts=&cursor_id=&limit=` → `{orders: [...], has_more, next_cursor}`
- `api.getPaperOrdersDelta(afterTS, filters)` → `GET :6060/api/paper-orders?after_ts=&...` → `{orders: [...]}` (new rows only)
- `api.getPaperOrdersInsights()` → `GET :6060/api/paper-orders-insights` → `{summaries, bands, insight_run_ts}`
- PaperOrder: `{id, ts, match_ticker, market_ticker, player_name, context, market_price, edge_cents, suggested_size, strategy, result, settled_ts}`

## State

- `selectedStrategies` — Set of strategy names (multi-toggle). Shared with insights via `bind:`. Auto-initialized to all strategies on first meta load.
- `minPrice` / `maxPrice` / `filterMatch` / `filterResult` — server-side filters. Changing any triggers `refetchAll()`.
- `orders` — accumulated orders from page fetches + delta prepends. Capped at 500.
- `summary` — server-computed aggregates (total row). Reflects active filters across ALL data, not just loaded page.
- `hasMore` / `nextCursor` — cursor pagination state for "Load More".
- `pendingOrders` / `settledOrders` — `$derived` split of `orders` by result presence.

## Filter Flow

1. User toggles strategy / changes price / types match / selects result
2. `$effect` detects filter state change → increments `filtersChanged`
3. Second `$effect` fires `refetchAll()` → parallel `getPaperOrdersSummary` + `getPaperOrdersPage`
4. Summary reflects ALL matching orders (server-side GROUP BY). Page shows first 100.

## Polling

- Delta poll (5s): `getPaperOrdersDelta(lastTS, filters)` → prepends new orders to `orders`. Refreshes summary if new rows arrived.
- Insights auto-refresh (5 min): calls `insightsComp.refresh()`.

## Layout

- `.layout` flex: `.main-content` (left, flex:1) + `.filter-sidebar` (right, 240px sticky)
- Sidebar groups: Strategies (shared toggle) + Filters + Insights (refresh button)
- Summary bar: server-computed total row (reflects active filters across ALL data)
- "Load More" button at bottom when `hasMore` is true

## Analysis — PaperOrdersInsights component

All analysis pre-computed by `internal/paperorderinsights` cron. No client-side recompute.

## Tables

- Open Positions: pending orders from loaded set, no P&L column
- Settled Trades: settled orders from loaded set, WON/LOST badge + P&L (computed client-side from `result` + `market_price` + `suggested_size`)
- "Load More" button appends next page via cursor. Caps at 500 rows total.

## Chart Colors

Same `strategyColors` map as `/simulation` page: matchpoint=#60a5fa, matchpoint-aggro=#a78bfa, setpoint=#34d399, setpoint-serve=#fbbf24, setpoint-cheap=#f472b0, fadelongshot=#f87171.
