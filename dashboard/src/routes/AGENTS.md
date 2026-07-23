# src/routes — SvelteKit Pages

File-based routing. Each route directory has `+page.svelte` (component) and `+page.js` (load function, SSR disabled).

## Routes

### `/` (root)
- `+page.js` — Redirects to `/matches` (307).

### `/matches`
- `+page.js` — Disables SSR, initial load of tracked markets + order counts.
- `+page.svelte` — Tracked matches table. Polls `getTracked()` (2s) + `getOrderCounts()` (5s). Click row → match detail. Stats: event count, market count. Table wrapped in `CollapsibleSection`.

### `/matches/[event_ticker]`
- `+page.js` — Disables SSR, initial load of tick data for event.
- `+page.svelte` — Match detail. Polls `getTicks(eventTicker)` (3s). Chart.js line chart with price data + order markers. Market cards with tick counts. Simulated orders table in `CollapsibleSection`. Real orders section (empty, collapsed by default).

### `/orders`
- `+page.js` — Disables SSR, initial load of `/api/orders?limit=100` + `/api/paper-orders-insights` in parallel.
- `+page.svelte` — Paper orders. Polls `getOrders({limit:100})` (5s) for tables. Pre-computed insights from `PaperOrdersInsights` component (manual refresh + 5-min auto). Filters: strategy (shared with insights), result, match, price (tables only). Summary bar from `filteredSummary` (page subset). Two `CollapsibleSection` tables: Open Positions (pending) + Settled Trades (recent 100). No "load more" pagination.

### `/strategies`
- `+page.js` — Disables SSR, initial load of `/api/strategies`.
- `+page.svelte` — Lists all registered strategies from `backtest.DefaultFactories()`. Groups variants by base name. No polling — static registry.

### `/simulation`
- `+page.js` — Disables SSR, initial load of strategy list.
- `+page.svelte` — Pre-computed simulation insights. Single `getSimulation()` call on mount (no polling — data updates daily via cron). Strategy toggles + day filter + chart metric selector. Summary cards per strategy (from `backtest_results.summary_json`). Four Chart.js charts: cumulative P&L (from pre-computed `cum_pnl_json`), win/loss bars, band performance, signal count per band. Peak band cards + cross-strategy band totals + best bands table + per-strategy per-band detail table. No live recompute — all data from `simulation_insights` + `backtest_results` tables.

### `/system`
- `+page.js` — Disables SSR, initial load of metrics.
- `+page.svelte` — System metrics. Imports `systemStore` singleton (persists across navigation). StatCards for goroutines, heap, GC, etc. Eight `LineChart` components for memory/goroutine/GC/malloc time series.

## Layout

- `+layout.svelte` — Nav bar (Matches, Paper Orders, Real Orders, Simulation, Config, System). Imports `systemStore` to start metrics polling on app load. Global CSS imports.

## Conventions

- `+page.js` always sets `export const ssr = false` — dashboard is client-only.
- Polling via `createPoll` from `$lib/poll.js` except system page uses singleton `systemStore`.
- All table sections wrapped in `CollapsibleSection`.
- Page titles via `<svelte:head><title>`.
