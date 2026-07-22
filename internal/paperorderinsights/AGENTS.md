# internal/paperorderinsights

Cron-driven pre-computation of paper order insights from the live `orders`
table (paper only, `is_real = false`).

## Files

- `compute.go` — `ComputeMissing(db, log)`: loads resolved paper orders,
  diffs days against `paper_order_insights`, computes per-strategy × per-day
  × per-band derived metrics + peak flags, persists. Also recomputes
  `paper_order_summaries` (per-strategy aggregates + cum_pnl series) every run.

## Source

Reads `orders` table directly via raw SQL join with `markets` for result.
NOT `backtest_results.orders_json` — that's the backtest cron's source.
Live orders may diverge (real emitter writes extra columns, legacy rows,
pending orders excluded by result filter).

## Tables

- `paper_order_insights` — per strategy × day × fixed band, derived metrics
  (sharpe, profit_factor, max_drawdown, score, peak). Mirrors
  `simulation_insights` shape.
- `paper_order_summaries` — per-strategy aggregate + `cum_pnl_json` series.
  Mirrors `backtest_results` summary shape (no orders_json, no match_count).

## Cron

Wired in `main.go` alongside `pricebands` cron. Runs at startup + every 24h.
Only missing days computed for insights; summaries recomputed every run
(small table, cheap, captures new orders without waiting for day rollover).

## Conventions

- Reuses `pricebands.FixedBands`, `BandLabel`, `FindBand`, `TSToDay` —
  single source of truth for band definitions.
- Derived metric logic mirrors `pricebands/insights.go` — same sharpe,
  profit_factor, max_drawdown, score formulas. Keep in sync if either changes.
- Paper orders only (`is_real = false`). Real orders excluded.
- Resolved orders only (market result non-empty). Pending excluded.
