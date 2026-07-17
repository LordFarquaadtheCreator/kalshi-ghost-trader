# /matches/[event_ticker] — Match Detail

Real-time tick price chart + market cards + simulated orders for a single event.

## Files

- `+page.js` — SSR disabled. Initial load: `getTicks(eventTicker)`.
- `+page.svelte` — Polls `getTicks()` (3s). Chart.js line chart with per-market price lines + order markers (yellow triangles). Market cards show tick count + last price. Simulated orders table in `CollapsibleSection`. Real orders section collapsed by default.

## Data

- `api.getTicks(eventTicker)` → `GET :6060/api/ticks?event=...` → `{event_ticker, title, markets: [{market_ticker, player_name, ticks: [{ts, price}]}], orders: [{ts, market_ticker, context, market_price, edge_cents, suggested_size, strategy}]}`

## Chart

- X axis: timestamp (linear scale)
- Y axis: price 0–1 (cents)
- Datasets: one per market (blue/pink lines) + order markers (yellow triangles)
