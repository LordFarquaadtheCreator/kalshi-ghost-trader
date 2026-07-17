# /matches — Tracked Matches

Lists currently tracked markets from ghost-trader. Links to match detail page.

## Files

- `+page.js` — SSR disabled. Initial load: `getTracked()` + `getOrderCounts()`.
- `+page.svelte` — Polls tracked (2s) + order counts (5s). Stats grid (events, markets). Two `CollapsibleSection` tables. Row click → `/matches/[event_ticker]`.

## Data

- `trackedStore` → `api.getTracked()` → `GET :6060/api/tracked` → `{subs, event_count, market_count, scores}`
- `countsStore` → `api.getOrderCounts()` → `GET :6060/api/order-counts` → `{counts: {event_ticker: count}}`
- `scores` map comes from `Engine.LatestScores()` which queries the `points` table (API-Tennis data).

## Live vs Upcoming Split

- **Live Matches** (expanded): rows where `scores[event_ticker]` exists. Means a point has been played — match is happening right now.
- **Upcoming Matches** (collapsed by default): rows with no score. Tracked but no points data yet — match hasn't started or API-Tennis hasn't pushed data.

This split is intentional. "Live" = a score has been played, not just "market is tracked". A market can be active on Kalshi (WS subscribed, ticks flowing) but not yet "live" in tennis terms (no points scored).

## Columns

Event Ticker, Match (formatted), Series (extracted), Market Ticker, Score, Sim Orders (from counts), Live Orders (from pending counts).
