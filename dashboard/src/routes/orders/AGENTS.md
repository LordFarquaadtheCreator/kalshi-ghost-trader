# /orders — Paper Orders

Simulated paper trading orders split into pending (open positions) and settled trades.
Filter surface mirrors `/simulation` (simulation insights) for parity.

## Files

- `+page.svelte` — No `+page.js`. Client-side polling via `createPoll(() => api.getOrders(), 5000)`.

## Data

- `api.getOrders()` → `GET :6060/api/orders` → `{orders: PaperOrder[], summary: PaperOrderSummary}`
- `PaperOrder`: `{ts, match_ticker, market_ticker, player_name, context, market_price, edge_cents, suggested_size, strategy, result, won, pnl}`
- `result` empty = pending, "yes" = won, else lost.

## State

- `selectedStrategies` — Set of strategy names (multi-toggle). Auto-initialized to all strategies on first data load.
- `minPrice` — drop orders with `market_price` below threshold (client-side, mirrors backtest `min_price`)
- `filterMatch` — substring filter on `match_ticker`
- `filterResult` — won / lost / pending / all
- `filteredOrders` — `$derived` applying all four filters, sorted by ts desc
- `settledOrders` — `$derived` from `filteredOrders.filter(o => o.result)`
- `pendingOrders` — `$derived` from `filteredOrders.filter(o => !o.result)`
- `filteredSummary` — `$derived` recomputes summary stats from `filteredOrders` (not from API summary)

## Layout

- `.layout` flex: `.main-content` (left, flex:1) + `.filter-sidebar` (right, 240px sticky)
- Sidebar groups: Strategies (toggle-all + per-strategy toggle buttons) + Filters (min price, match, result)
- Mirrors `/simulation` page sidebar structure for visual parity

## UI

- Summary bar: uses `filteredSummary` (reflects active filters)
- Filter count line above main content
- Open Positions table: `CollapsibleSection`, pending orders only, no P&L column
- Settled Trades table: `CollapsibleSection`, resolved orders with WON/LOST badge + P&L
- Strategy toggles reuse `strategyColors` map from `/simulation` page; fallback `vibrantColor(name)`

## Charts

Six Chart.js charts in Analysis `CollapsibleSection`:
1. Cumulative P&L — line, settled orders sorted by ts
2. P&L by Strategy — bar, net pnl per strategy
3. Win / Loss by Strategy — stacked bar
4. Entry Price Distribution — bar, 10 bins (0-100c)
5. Orders by Day — mixed: count bars (left y) + net P&L line (right y). Day = YYYY-MM-DD from `o.ts`
6. Orders by Hour (24hr) — mixed: count bars + P&L line. Hour = 0-23 from `new Date(o.ts).getHours()`

`bucketByDay` / `bucketByHour` helpers group filtered orders. Count bars use all filtered orders; P&L line sums `o.pnl` for settled orders only (pending orders contribute to count, not P&L).

## Chart Colors

Same `strategyColors` map as `/simulation` page: matchpoint=#60a5fa, matchpoint-aggro=#a78bfa, setpoint=#34d399, setpoint-serve=#fbbf24, setpoint-cheap=#f472b0, fadelongshot=#f87171.
