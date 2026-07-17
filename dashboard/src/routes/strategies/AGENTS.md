# /strategies — Simulated Outcomes

Backtest results across strategies with charts and per-strategy order detail.

## Files

- `+page.js` — SSR disabled. Initial load: `getStrategies()`.
- `+page.svelte` — Strategy toggle buttons + min price filter. Runs `runBacktest()` on load and on recompute button click.

## Data

- `api.getStrategies()` → `GET :6061/api/strategies` → `{strategies: string[]}`
- `api.runBacktest(strategies, minPrice)` → `POST :6061/api/backtest` → `{results: [{name, summary, orders}]}`
- `summary`: `{total_signals, win_rate, net_pnl, roi, sharpe, profit_factor, avg_edge, max_drawdown, wins, losses}`
- `orders`: `[{match, context, price, edge_cents, size, won, pnl}]`

## State

- `strategies` — list of available strategy names
- `selected` — Set of selected strategy names
- `results` — `{name: result}` map from last backtest run
- `minPrice` — minimum price filter for backtest
- `filterResult` / `filterMatch` — filters for per-strategy order tables

## UI

- Strategy toggle buttons with color dots
- Min price input + Recompute button
- Summary cards per strategy (8 stats each)
- Three Chart.js charts: cumulative P&L (line), win/loss (bar), price distribution (bar)
- Orders Detail: filters + per-strategy `CollapsibleSection` tables (50 rows max)

## Chart Colors

Defined in `strategyColors` map: matchpoint=#60a5fa, matchpoint-aggro=#a78bfa, setpoint=#34d399, setpoint-serve=#fbbf24, setpoint-cheap=#f472b0, fadelongshot=#f87171.
