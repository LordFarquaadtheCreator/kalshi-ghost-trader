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
- `+page.svelte` — Paper orders. Polls `getOrders()` (5s). Filters: strategy, result (won/lost/pending). Summary bar computed from filtered orders (`filteredSummary`). Two `CollapsibleSection` tables: Open Positions (pending) + Settled Trades. No `+page.js` — uses client-side polling only.

### `/simulation`
- `+page.js` — Disables SSR, initial load of strategy list.
- `+page.svelte` — Pre-computed simulation insights. Single `getSimulation()` call (5-min poll). Strategy toggles + day filter + chart metric selector. Summary cards per strategy (from `backtest_results.summary_json`). Four Chart.js charts: cumulative P&L (from pre-computed `cum_pnl_json`), win/loss bars, band performance, signal count per band. Peak band cards + cross-strategy band totals + best bands table + per-strategy per-band detail table. No live recompute — all data from `simulation_insights` + `backtest_results` tables.

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
