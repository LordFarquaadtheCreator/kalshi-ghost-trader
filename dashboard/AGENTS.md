# Dashboard

SvelteKit frontend for Kalshi Ghost Trader. Serves from `localhost:5173` (dev) or static build.

## Stack

- SvelteKit 2 + Svelte 5 (runes: `$state`, `$derived`, `$props`)
- Vite for build/bundling
- Chart.js for charts (lazy-loaded, singleton via `chart-init.js`)
- Plain CSS design system in `src/lib/styles.css` (no Tailwind)

## Structure

- `src/lib/` — shared utilities, API client, stores, components
- `src/routes/` — SvelteKit pages (file-based routing)
- `src/app.css` — global reset + CSS variable imports
- `vite.config.js` — build config with manual chunk splitting

## Commands

```bash
npm run dev      # dev server on :5173
npm run build    # production build to build/
npm run check    # svelte-check (types + a11y)
npm run preview  # preview production build
```

## Conventions

- No TypeScript. JSDoc `@type` annotations for type safety.
- Svelte 5 runes only — no legacy stores except `readable` in `poll.js`/`system-store.js`.
- Shared components in `src/lib/components/` — always import from there, don't duplicate.
- CSS variables defined in `src/lib/styles.css` — use `var(--name)`, don't hardcode colors.
- Each page has a `+page.js` for SSR disable + initial data load.
- Polling stores: `createPoll` for generic, `systemStore` singleton for metrics (persists across navigation).

## Backend Dependencies

- `localhost:6060` — ghost-trader: metrics, tracked markets, strategy API (backtest, ticks, orders)

## Pages

| Route | Description |
|---|---|
| `/matches` | Tracked matches table, links to match detail |
| `/matches/[event_ticker]` | Match detail: price chart, market cards, sim orders |
| `/orders` | Paper orders: open positions + settled trades, filters, summary |
| `/simulation` | Pre-computed backtest insights: strategy summaries, cumulative P&L, band performance, peaks |
| `/system` | System metrics: Go runtime stats, memory/GC charts (singleton store) |
