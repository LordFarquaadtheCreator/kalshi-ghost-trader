# src/lib — Shared Library

Shared utilities, API client, stores, and components used across all pages.

## Files

- `api.js` — API client with caching + mutation helper. Base URLs: `GHOST_TRADER_URL` and `STRATEGY_API_URL` (both empty string — same host, proxied by Vite dev server). Methods:
  - Reads (cached, TTL-bounded): `getMetrics`, `getTracked`, `getOrderCounts`, `getPendingOrderCounts`, `getPassedMatches`, `getOrders({limit, cursor_ts, cursor_id})`, `getTicks(eventTicker)`, `getStrategies`, `getSimulation`, `getPaperOrdersInsights`, `getRealOrders`, `getLiquidityPool`, `getStrategyConfig`, `getAppConfig`, `getTriggerRanges(strategy?)`.
  - Mutations (POST/PUT, clears entire cache on success): `resetLiquidityPool(cents)`, `topUpLiquidityPool(cents)`, `setStrategyEnabled(name, enabled)`, `setAppConfig(key, value)`, `replaceTriggerRanges(strategy, ranges)`.
  - `pollInterval(base)` — exponential backoff on consecutive failures (caps at 30s).
- `poll.js` — Generic polling store (`createPoll`) and legacy `createMetricsPoll`. Uses Svelte `readable` store. Pauses on tab hidden, exponential backoff on error.
- `system-store.js` — Module-level singleton for system metrics polling. Persists across navigation — imported in `+layout.svelte` so polling starts on app load. 1s interval, 120 sample rolling window.
- `chart-init.js` — Chart.js singleton loader. Lazy-loads chart.js on first call, registers controllers. Returns Chart constructor or null (SSR).
- `stats.js` — Statistical helpers (mean, median, std dev, variance, skewness, kurtosis) used by `StatAnalysis.svelte`.
- `utils.js` — Formatting helpers: `fmtTime`, `fmtTicker`, `seriesFromTicker`, `fmtPnL`, `fmtPct`, `fmtBytes`, `fmtNum`.
- `styles.css` — CSS design system. Variables for colors (`--win`, `--loss`, `--surface`, `--border`, etc.), spacing, radius. Component classes: `.data-table`, `.summary-bar`, `.stats-grid`, `.filters`, `.page-container`, `.pnl-win`, `.pnl-loss`, `.row-win`, `.row-loss`.
- `index.js` — Barrel export (unused, legacy).
- `app.css` — Global reset + imports `styles.css` variables.

## Components

All in `components/` directory:

- `PageHeader.svelte` — Page title + connection status + error badge. Snippet slot for extra badges.
- `StatCard.svelte` — Single stat: label + value. Used in stats grids.
- `Badge.svelte` — Colored pill. Variants: `ok`, `err`, `pending`, `loading`.
- `EmptyState.svelte` — Centered placeholder text. Variants: default, `error`.
- `DataTable.svelte` — Generic table from column config + row data. Supports clickable rows, alignment, custom classes.
- `LineChart.svelte` — Chart.js line chart bound to a polling store's history. Series config with `getValue` + `color`.
- `BarChart.svelte` — Chart.js bar chart. Used for win/loss comparison.
- `CollapsibleSection.svelte` — Collapsible wrapper with title + count. Props: `title`, `count`, `defaultOpen`. Used for all table sections.
- `StatAnalysis.svelte` — Statistical analysis of an order set (mean/median/std dev/skewness/kurtosis/min/max P&L). Uses `computeStats` from `$lib/stats.js`. Wrapped in `CollapsibleSection`.
- `PaperOrdersInsights.svelte` — Pre-computed paper order analysis (summary cards, charts, band tables). Props: `data` (load function output), `selectedStrategies` (bindable), `strategyColors`. Exposes `refresh(fetcher)` method via `bind:this`. Mirrors `/simulation` page analysis section.

## Conventions

- Components use Svelte 5 runes (`$props`, `$state`).
- No TypeScript — JSDoc `@type` annotations.
- CSS uses variables from `styles.css` — no hardcoded colors.
