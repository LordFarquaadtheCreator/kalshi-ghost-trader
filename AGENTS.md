# Kalshi Ghost Trader

Go service tracking Kalshi tennis match markets in real-time via WebSocket,
storing every price/trade/lifecycle message to PostgreSQL for algorithm testing.

## Build

Compiled binaries go in `bin/` (gitignored). Never leave binaries in repo root.

```bash
mkdir -p bin
go build -o bin/ghost-trader .
go build -o bin/backtest ./cmd/backtest
go vet ./...
```

## Run

Two-layer config:
- **`app.yaml` / `app.dev.yaml`** — technical config (environment, credentials, paths). See `internal/appconfig/`.
- **`app_config` DB table** — runtime tunables (intervals, strategy params, bankroll). Dashboard-editable. See `internal/runtimeconfig/`.

```bash
# First-time setup:
cp app.dev.yaml.example app.dev.yaml   # dev (demo keys)
# OR
cp app.yaml.example app.yaml           # prod (real keys)
# Edit: set kalshi_api_key_id, kalshi_private_key_path, environment, db_dsn

# app_config, liquidity_pool, strategy_config, trigger_ranges seeded automatically
# by SQL migrations on first startup. No manual seeding needed.

# Run (dev — auto-selects app.dev.yaml if present):
go run .

# Run (prod — explicit):
APP_ENV=prod go run .
```

## Monitoring

```bash
# Runtime metrics + pprof (built into app, port 6060)
curl http://127.0.0.1:6060/metrics
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30

# External resource monitor (CPU, RSS, network IO, Go runtime)
./scripts/monitor.sh $(pgrep -f ghost-trader) 2 metrics.csv
```

## Architecture

Each package has its own `AGENTS.md` with package-specific gotchas.

`cmd/` contains commands ONLY — executable entrypoints, no library code. Shared logic belongs in `internal/`.

- `main.go` — entrypoint, signal handling, errgroup wiring
- `cmd/backtest/` — replay historical data through trading strategies
- `cmd/backfill-orders/` — one-shot CLI: backfill stale real order status + resolve markets with results but unresolved orders
- `internal/appconfig/` — YAML config loading (env, credentials, paths)
- `internal/runtimeconfig/` — `app_config` DB table CRUD (runtime tunables, dashboard-editable)
- `internal/config/` — global `Config` combining `appconfig.EnvConfig` + `runtimeconfig.RuntimeConfig`; `config.Cfg` package-level singleton
- `internal/kalshiAuth/` — RSA-PSS-SHA256 request signing (PKCS#8 + PKCS#1)
- `internal/kalshiclient/` — REST client (events, markets, pagination, rate limit)
- `internal/store/` — PostgreSQL (GORM, single writer via TickWriter, batched tick inserts, app_config, orders, liquidity_pool, strategy_config, trigger_ranges, positions)
- `internal/ws/` — WebSocket manager (auto-reconnect, re-subscribe, dispatch)
- `internal/scanner/` — daily series scan, stores new events/markets
- `internal/tracker/` — market subscription lifecycle (no per-match goroutine)
- `internal/scheduler/` — schedules tracking at occurrence_datetime - lead
- `internal/reconciler/` — resolves market results, settles orders
- `internal/schedulechecker/` — validates scheduled match tracking
- `internal/backtest/` — backtest engine, result persistence, cumulative P&L series
- `internal/apitennis/` — API-Tennis WebSocket real-time scraper (optional, primary score source)
- `internal/kalshilivedata/` — Kalshi live-data REST poller (optional, backup score source)
- `internal/algorithms/` — pluggable trading strategies (match-point detection, order emission)
- `internal/strategies/` — `Build()` factory wiring all strategies into `MultiStrategyRuntime`
- `internal/strategyconfig/` — `strategy_config` table CRUD (per-strategy real-trading enable)
- `internal/triggerranges/` — `trigger_ranges` table CRUD (per-strategy price bands for real orders)
- `internal/liquiditypool/` — liquidity pool (single source of truth for real cash, kelly sizing reads balance live)
- `internal/positions/` — position lifecycle for sell-to-close pipeline (one Position per market×strategy×is_real)
- `internal/signal/` — close-timer strategy, simulated order emission
- `internal/pricebands/` — fixed-band price analysis cron (computes missing days, persists to `price_band_results` + `simulation_insights`)
- `internal/paperorderinsights/` — cron-driven pre-computation of paper order insights (per-strategy × per-day × per-band aggregates + summaries from live orders table)
- `internal/orderbackfill/` — background poller refreshing stale real order status from Kalshi REST
- `internal/dashboardapi/` — HTTP server (metrics, pprof, REST API for dashboard)
- `internal/dashboarddata/` — live DB queries for dashboard (separate from backtest replay engine)
- `dashboard/` — SvelteKit + Vite dashboard (real orders, liquidity pool, config management, charts)

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single PostgreSQL writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One Reconciler goroutine: polls DB for unresolved markets, REST fetches results
- One OrderBackfill goroutine: polls DB for stale real orders, REST refreshes status
- One ScheduleChecker goroutine: polls upcoming markets, REST refreshes occurrence_ts
- One API-Tennis goroutine (if enabled): WS read loop, per-match dispatch
- One goroutine per active match (if Kalshi live-data enabled): REST poll loop
- One goroutine per scheduled match: waits until start time, then subscribes
- One MultiStrategyRuntime timer goroutine: drives periodic `OnTick` calls (close_timer etc)
- One Backtest recompute cron goroutine: daily midnight UTC, recomputes if new finalized markets
- One pricebands cron goroutine: computes missing days daily, persists to `price_band_results` + `simulation_insights` (derived per-band metrics: sharpe, profit_factor, max_drawdown, score, peak)
- One paperorderinsights cron goroutine: computes missing days daily from live orders table (paper only), persists to `paper_order_insights` + `paper_order_summaries`
- One dashboardapi HTTP server goroutine: serves metrics + REST API on `metrics_addr` (default `127.0.0.1:6060`)

## PostgreSQL Schema

- `events` — tennis match events (1 per match). `coverage` tag set at settlement.
- `markets` — 2 markets per event (one per player). FK to events, triggers handle cascade.
- `ticks` — every WS message (ticker, trade) with raw JSON payload. Log table, no FK.
- `orderbook_events` — orderbook snapshots + deltas with raw JSON payload.
- `lifecycle_events` — market_lifecycle_v2 WS events.
- `event_lifecycle_events` — event_lifecycle WS messages (event creation announcements).
- `kalshi_scores` — live score snapshots from Kalshi /live_data (backup score source).
- `points` — point-by-point score data from API-Tennis (primary score source).
- `scan_runs` — scan audit log.
- `orders` — simulated + real orders from strategy signals. No FK. Traceable via match_ticker + market_ticker. Includes `match_title` and `player_name` columns (populated by real emitter, empty for legacy/paper rows).
- `positions` — one row per (market_ticker, strategy, is_real). Tracks FilledBuyCount/FilledSellCount/AvgEntryPrice/AvgExitPrice for sell-to-close pipeline.
- `backtest_results` — per-strategy backtest summary + orders + cumulative P&L series (JSON). One row per strategy.
- `price_band_results` — per-day per-strategy per-fixed-band aggregates. Populated by pricebands cron.
- `simulation_insights` — per-day per-strategy per-fixed-band derived metrics (sharpe, profit_factor, max_drawdown, score, peak). Populated by pricebands cron alongside `price_band_results`.
- `paper_order_insights` — per-day per-strategy per-fixed-band derived metrics for live paper orders. Populated by paperorderinsights cron.
- `paper_order_summaries` — per-strategy aggregate + cumulative P&L series for live paper orders. Populated by paperorderinsights cron.
- `app_config` — runtime tunables KV store. Seeded by migration `0002_seed_app_config.sql`.
- `app_config_history` — change tracking for app_config.
- `liquidity_pool` — single row (id=1). Single source of truth for real cash. Kelly sizing reads `balance_cents` live.
- `strategy_config` — per-strategy enable/disable flag for real trading.
- `trigger_ranges` — per-strategy price bands gating real order emission.

Cascade deletes use PL/pgSQL trigger functions (see `internal/store/`):
- `trg_markets_delete_cascade` — cleans ticks, orderbook, lifecycle on market delete.
- `trg_events_delete_cascade` — cleans markets, event_lifecycle, orders on event delete.

Coverage classification on events at settlement:
- `full` — ≥100 ticks spanning ≥290s in final 5-min pre-close window.
- `low_freq` — 1-99 ticks in that window.
- `none` — no ticks (auto-pruned on settlement by P6).

Payload retention: non-`full` events have `payload` NULLed in ticks/orderbook at settlement (P7).
Orphan janitor (`CleanOrphans`) and late-parenting sweep (`AdoptOrphans`) run after each scan cycle.

## Deployment (Linux Mint box)

Scraper runs on Linux Mint box on LAN — same machine as `ssh mint` (see below).
Not a cloud instance. DB lives on local disk there.

```bash
ssh mint                                    # alias in ~/.ssh/config
ssh fahad@192.168.1.246                     # direct
```

Key: `~/.ssh/id_ed25519` (copied via `ssh-copy-id`). Host: `linux-mint`,
Linux Mint 24.04, x86_64. Passwordless sudo granted for `systemctl` + `journalctl`
(via `/etc/sudoers.d/fahad-systemctl`).

### systemd services

- `kalshi-ghost-trader.service` — backend binary, `Restart=always`, `APP_ENV=prod`
- `kalshi-dashboard.service` — Vite dev server, `BindsTo` backend

Unit files: `/etc/systemd/system/kalshi-{ghost-trader,dashboard}.service`
Repo: `/home/fahad/kalshi-ghost-trader`
DB: PostgreSQL `kalshi_tennis` on `127.0.0.1:5432`
Ports: backend `6060` (all interfaces), dashboard `5173` (all interfaces)

```bash
ssh mint 'sudo -n systemctl status kalshi-ghost-trader --no-pager -n 20'
ssh mint 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'
ssh mint 'sudo -n systemctl restart kalshi-ghost-trader kalshi-dashboard'
```

### Update workflow

```bash
# Build locally, scp artifacts, sync service file, restart
./deploy/deploy.sh mint main
```

If schema changed, run migration first.

## Snapshots (Remote → Local)

`scripts/snapshot.sh` exports gzipped JSON summaries + backtest output
to `snapshots/YYYYMMDD_HHMM/`. `scripts/fetch-snapshots.sh` rsyncs them locally to `snapshots/`.

Exports:
- `orders.json.gz` — all simulated orders with computed P&L
- `orders_unresolved.json.gz` — orders without market result yet
- `events_summary.json.gz` — events with coverage, market status, tick counts
- `strategy_summary.json.gz` — per-strategy aggregates (win rate, ROI, P&L)
- `tick_stats.json.gz` — tick counts per market (top 500)
- `scan_runs.json.gz` — scan audit log
- `lifecycle_summary.json.gz` — recent market lifecycle transitions
- `points_summary.json.gz` — point-by-point score data summary
- `db_stats.json.gz` — table row counts, DB file size
- `backtest.txt.gz` — full `ghost-trader backtest -strategy all` output
- `meta.json.gz` — snapshot timestamp, uptime, goroutine count, heap

Tiered retention (both remote + local):
- 0–48h: keep all snapshots (8 at 6h intervals)
- 2–30 days: keep 1 per day
- 30–90 days: keep 1 per week
- 90+ days: delete

Run manually (no cron currently configured on mint):
```bash
ssh mint '/home/fahad/kalshi-ghost-trader/scripts/snapshot.sh'
```

Fetch locally:
```bash
./scripts/fetch-snapshots.sh 192.168.1.246
```

Inspect:
```bash
zcat snapshots/<dir>/strategy_summary.json.gz | python3 -m json.tool
zcat snapshots/<dir>/backtest.txt.gz
```

## Backup

On mint — manual full DB backup (no cron currently configured):
```bash
ssh mint 'pg_dump -U kalshi kalshi_tennis | gzip > /home/fahad/kalshi-ghost-trader/backups/kalshi_$(date +%Y%m%d).sql.gz'
ssh mint 'find /home/fahad/kalshi-ghost-trader/backups/ -name "kalshi_*.sql.gz" -mtime +7 -delete'
```

Locally — atomic snapshot while scraper running:
```bash
mkdir -p backups
pg_dump -U kalshi kalshi_tennis | gzip > backups/kalshi_tennis_$(date +%Y%m%d_%H%M).sql.gz
```

## Tennis Series

12 core match-winner series:
- KXATPMATCH, KXWTAMATCH (main tour singles)
- KXITFMATCH, KXITFWMATCH (ITF singles)
- KXATPCHALLENGERMATCH, KXWTACHALLENGERMATCH (Challenger singles)
- KXTENNISEXHIBITION, KXCHALLENGERMATCH (exhibition/legacy singles)
- KXATPDOUBLES, KXWTADOUBLES (main tour doubles)
- KXITFDOUBLES, KXITFWDOUBLES (ITF doubles)

## Python (notebooks)

Conda env for analysis notebooks in `research/`.

```bash
conda activate kalshi-ghost-trader
# Recreate from scratch:
conda env create -f environment.yml
```

Notebooks query the live PostgreSQL DB read-only. Never open the DB for writes from notebooks.

## Backtest

Replay historical tick data through a strategy and report P&L.

```bash
go run ./cmd/backtest -strategy matchpoint
go run ./cmd/backtest -strategy matchpoint -debug   # log filter reasons
```

Strategies register in `DefaultFactories()` in `internal/backtest/factories.go` —
single source of truth for CLI + dashboard + live. Must implement `replayStrategy`
(Strategy + `SetReplayTime` + `OnPriceAt`).

## Price Band Analysis + Simulation Insights

Cron goroutine in `internal/pricebands/` runs daily, computes days not
yet in `price_band_results` + `simulation_insights` tables, persists:

- `price_band_results` — per-day per-strategy per-fixed-band aggregates (n, wins, win_rate, net_pnl, invested, roi, avg_edge)
- `simulation_insights` — same grouping + derived metrics (sharpe, profit_factor, max_drawdown, score, peak flag)

Dashboard `/simulation` page reads pre-computed data from both tables +
`backtest_results` (summary + cumulative P&L series). No live recompute
on page load — all data served from persisted cron output.

Only missing days are computed — most runs find 0-1 new days.

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```

## Temp Scripts

One-off scripts (analysis, data exports, throwaway code) go in `temp-scripts/`. Gitignored. Never write to `/tmp` or other external paths. Remove temp files when done.

## Simulated Trades

Simulated trades **always** run all strategies. No strategy skipping, filtering, or conditional activation.
Every strategy registered in the system participates in every match — no exceptions.
This ensures complete paper-trail data for comparison and backtesting.
