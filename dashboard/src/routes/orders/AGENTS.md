# /orders — Paper Orders

Simulated paper trading orders split into pending (open positions) and settled trades.
Filter surface mirrors `/strategies` (simulated outcomes) for parity.

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
- Mirrors `/strategies` page sidebar structure for visual parity

## UI

- Summary bar: uses `filteredSummary` (reflects active filters)
- Filter count line above main content
- Open Positions table: `CollapsibleSection`, pending orders only, no P&L column
- Settled Trades table: `CollapsibleSection`, resolved orders with WON/LOST badge + P&L
- Strategy toggles reuse `strategyColors` map from `/strategies` page; fallback `vibrantColor(name)`

## Chart Colors

Same `strategyColors` map as `/strategies` page: matchpoint=#60a5fa, matchpoint-aggro=#a78bfa, setpoint=#34d399, setpoint-serve=#fbbf24, setpoint-cheap=#f472b0, fadelongshot=#f87171.
