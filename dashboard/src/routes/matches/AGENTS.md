# /matches — Tracked Matches

Lists currently tracked markets from ghost-trader. Links to match detail page.

## Files

- `+page.js` — SSR disabled. Initial load: `getTracked()` + `getOrderCounts()`.
- `+page.svelte` — Polls tracked (2s) + order counts (5s). Stats grid (events, markets). Table in `CollapsibleSection`. Row click → `/matches/[event_ticker]`.

## Data

- `trackedStore` → `api.getTracked()` → `GET :6060/api/tracked` → `{subs, event_count, market_count}`
- `countsStore` → `api.getOrderCounts()` → `GET :6060/api/order-counts` → `{counts: {event_ticker: count}}`

## Columns

Event Ticker, Match (formatted), Series (extracted), Market Ticker, Sim Orders (from counts), Real Orders (always 0).
