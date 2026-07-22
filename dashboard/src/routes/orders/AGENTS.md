# /orders — Paper Orders

Paper trading orders with pre-computed analysis (mirrors `/simulation` architecture).

## Files

- `+page.js` — SSR disabled. Initial load: `/api/orders?limit=100` + `/api/paper-orders-insights` in parallel.
- `+page.svelte` — Polls `/api/orders` (5s, limit 100) for tables. Insights from `+page.js` load + manual refresh + 5-min auto-refresh. Analysis rendered by `PaperOrdersInsights` component.

## Data

- `api.getOrders({limit:100})` → `GET :6060/api/orders` → `{orders, summary, strategies, has_more, next_cursor}`
- `api.getPaperOrdersInsights()` → `GET :6060/api/paper-orders-insights` → `{summaries, bands, insight_run_ts}`
- PaperOrder: `{ts, match_ticker, market_ticker, player_name, context, market_price, edge_cents, suggested_size, strategy, result, won, pnl}`

## State

- `selectedStrategies` — Set of strategy names (multi-toggle). Shared between tables + insights via `bind:`. Auto-initialized to all strategies on first data load.
- `minPrice` / `maxPrice` / `filterMatch` / `filterResult` — table-only filters (not applied to insights).
- `filteredOrders` — `$derived` applying table filters to polled page, sorted by ts desc.
- `settledOrders` / `pendingOrders` — `$derived` from `filteredOrders`.
- `filteredSummary` — `$derived` recomputes summary stats from `filteredOrders` (page subset only).

## Layout

- `.layout` flex: `.main-content` (left, flex:1) + `.filter-sidebar` (right, 240px sticky)
- Sidebar groups: Strategies (shared toggle) + Filters (tables only) + Insights (refresh button)
- Summary bar: `filteredSummary` (reflects active table filters, page subset only)

## Analysis — PaperOrdersInsights component

All analysis pre-computed by `internal/paperorderinsights` cron. No client-side recompute.

- Summary cards per strategy (signals, win rate, net P&L, ROI, sharpe, profit factor, avg edge, max DD)
- Cumulative P&L chart (from `cum_pnl` series per strategy)
- Win/Loss bar chart (from summaries)
- Band performance chart (from `bands`, metric selectable)
- Signal count per band (stacked bars)
- Peak band cards (local maxima above median score)
- Cross-strategy band totals table
- Best bands table (N ≥ minN, WR ≥ minWR)
- Per-strategy per-band detail table

## Tables

- Open Positions: pending orders from polled page, no P&L column
- Settled Trades (recent): settled orders from polled page (max 100), WON/LOST badge + P&L
- No "load more" pagination. Recent 100 rows only. Full history in `/matches/[event]` or `/simulation`.

## Chart Colors

Same `strategyColors` map as `/simulation` page: matchpoint=#60a5fa, matchpoint-aggro=#a78bfa, setpoint=#34d399, setpoint-serve=#fbbf24, setpoint-cheap=#f472b0, fadelongshot=#f87171.
