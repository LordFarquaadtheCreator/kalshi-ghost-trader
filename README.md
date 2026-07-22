# Kalshi Ghost Trader

Go service that tracks Kalshi tennis match markets in real-time over WebSocket
and stores every price, trade, and lifecycle message to PostgreSQL for algorithm
testing.

## What it does

- Scans Kalshi daily for tennis match-winner markets (ATP, WTA, ITF, Challenger, exhibition, doubles)
- Schedules WebSocket tracking a few minutes before each match starts
- Streams ticker, trade, orderbook, and lifecycle events into PostgreSQL
- Runs pluggable strategies that emit simulated (paper) orders + optional real IOC orders
- Pre-computes per-strategy price-band insights + paper order summaries via cron
- Exposes runtime metrics + pprof + REST API on `127.0.0.1:6060`
- Ships a SvelteKit dashboard for live charts, orders, simulation insights, config management

## Requirements

- Go 1.22+ ([install](https://go.dev/doc/install))
- PostgreSQL 14+ (`kalshi_tennis` database)
- Kalshi account with API access ([api keys](https://kalshi.com/api-keys))
- RSA private key (PEM) for request signing

## Setup

1. Clone:

   ```bash
   git clone <repo-url>
   cd kalshi-ghost-trader
   ```

2. Copy config template:

   ```bash
   cp app.dev.yaml.example app.dev.yaml   # dev (demo keys)
   # OR
   cp app.yaml.example app.yaml           # prod (real keys)
   ```

3. Edit `app.dev.yaml` (or `app.yaml`). Required fields:

   ```yaml
   environment: demo                       # demo or prod
   kalshi_api_key_id: "your-key-id"
   kalshi_private_key_path: "/absolute/path/to/private_key.pem"
   db_dsn: "host=127.0.0.1 user=kalshi password=kalshi_dev dbname=kalshi_tennis port=5432 sslmode=disable"
   metrics_addr: "127.0.0.1:6060"
   rest_base_url: "https://external-api.demo.kalshi.co/trade-api/v2"
   ws_url: "wss://external-api-ws.demo.kalshi.co/trade-api/ws/v2"
   ```

   Use `demo` first. Switch to `prod` once everything works.

4. Put your Kalshi RSA private key on disk at the path you set above.

5. Create the PostgreSQL database:

   ```bash
   createdb -U kalshi kalshi_tennis
   ```

   Schema + seed data (`app_config`, `liquidity_pool`, `strategy_config`, `trigger_ranges`)
   are applied automatically by embedded SQL migrations on first startup.

## Run

```bash
# Dev (auto-selects app.dev.yaml if present):
go run .

# Prod (explicit):
APP_ENV=prod go run .
```

Logs go to stdout. Ctrl+C stops cleanly (flushes DB writer, unsubscribes WS).

## Configuration

Two layers:

- **`app.yaml` / `app.dev.yaml`** — env, credentials, paths. Read once at startup. See `internal/appconfig/AGENTS.md`.
- **`app_config` DB table** — runtime tunables (intervals, strategy params, bankroll). Dashboard-editable. See `internal/runtimeconfig/AGENTS.md`.

File selection: `APP_ENV=dev` → `app.dev.yaml`, `APP_ENV=prod` → `app.yaml`,
unset → `app.dev.yaml` if it exists else `app.yaml`.

Env fields (in `app*.yaml`):

| Field | Default | What it does |
|---|---|---|
| `environment` | `demo` | `demo` or `prod` |
| `kalshi_api_key_id` | — | Kalshi API key ID (required) |
| `kalshi_private_key_path` | — | Path to RSA private key PEM (required) |
| `db_dsn` | — | PostgreSQL DSN (required) |
| `metrics_addr` | `127.0.0.1:6060` | metrics/pprof/REST API bind address |
| `rest_base_url` | — | Kalshi REST API base URL (required) |
| `ws_url` | — | Kalshi WebSocket URL (required) |
| `apitennis_api_key` | — | API-Tennis external API key |
| `disable_ws_data_save` | `false` | Skip persisting Kalshi WS ticks/orderbook/lifecycle to DB |
| `backtest_cache_ttl_min` | `30` | Backtest cache TTL in minutes |

Runtime tunables (in `app_config` DB table, dashboard-editable): `series_tickers`,
`scan_interval_hours`, `track_lead_minutes`, `ws_*_backoff_secs`, `batch_size`,
`flush_timeout_ms`, `http_timeout_secs`, `rate_limit_rps`, `scheduler_poll_secs`,
`apitennis_timezone`, `kalshi_livedata_enabled`, `kalshi_livedata_poll_secs`,
`close_timer_*`, `reconciler_interval_secs`, `schedule_checker_interval_secs`,
`order_quota_*`, `per_strategy_cooldown_secs`, `real_trading_enabled`,
`kelly_fraction`, `paper_bankroll`, `real_bankroll`, `real_order_*`.

Full list in `internal/runtimeconfig/AGENTS.md`.

## Dashboard

SvelteKit app showing live runtime charts, orders, simulation insights, config.

```bash
cd dashboard
npm install
npm run dev
```

Opens at `http://localhost:5173`. Reads metrics + REST API from the Go service at
`http://127.0.0.1:6060`. See `dashboard/AGENTS.md`.

Pages:
- `/matches` — tracked matches (live + upcoming)
- `/matches/[event_ticker]` — match detail: price chart, market cards, sim orders
- `/orders` — paper orders + pre-computed insights
- `/real-orders` — real orders + liquidity pool management
- `/simulation` — pre-computed backtest insights, cumulative P&L, band performance
- `/config` — app_config + strategy_config + trigger_ranges editor
- `/system` — Go runtime metrics, memory/GC charts

## Monitoring

```bash
# Runtime metrics
curl http://127.0.0.1:6060/metrics

# Heap profile
go tool pprof http://127.0.0.1:6060/debug/pprof/heap

# CPU profile (30s)
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30

# External resource monitor (CPU, RSS, network IO)
./scripts/monitor.sh $(pgrep -f ghost-trader) 2 metrics.csv
```

## Build from source

```bash
mkdir -p bin
go build -o bin/ghost-trader .
go build -o bin/backtest ./cmd/backtest
go vet ./...
```

Binaries land in `bin/` (gitignored). Never commit binaries to repo root.

```bash
./bin/ghost-trader
```

## Deploy

Deployment artifacts in `deploy/`. Targets Linux Mint box on LAN (`ssh mint`).

```bash
./deploy/deploy.sh mint main     # build linux/amd64, scp, sync service, restart
```

See `deploy/AGENTS.md` for first-time remote setup (`deploy/setup-remote.sh`).

## Database

PostgreSQL `kalshi_tennis`. Single writer via TickWriter goroutine, batched
inserts. GORM for ORM, embedded SQL migrations in `internal/store/migrations/`.

Core tables:

- `events` — tennis match events (1 per match)
- `markets` — 2 markets per event (one per player)
- `ticks` — every WS ticker/trade message with raw JSON
- `orderbook_events` — orderbook snapshots + deltas with raw JSON
- `lifecycle_events` — `market_lifecycle_v2` WS events
- `event_lifecycle_events` — `event_lifecycle` WS messages (event creation)
- `points` — point-by-point score data from API-Tennis (primary)
- `kalshi_scores` — live score snapshots from Kalshi /live_data (backup)
- `orders` — simulated + real orders from strategy signals
- `positions` — one row per (market_ticker, strategy, is_real) for sell-to-close
- `scan_runs` — scan audit log
- `backtest_results` — per-strategy backtest summary + orders + cumulative P&L
- `price_band_results` / `simulation_insights` — pricebands cron output
- `paper_order_insights` / `paper_order_summaries` — paperorderinsights cron output
- `app_config` / `app_config_history` — runtime tunables + change tracking
- `liquidity_pool` — single row, single source of truth for real cash
- `strategy_config` — per-strategy real-trading enable flag
- `trigger_ranges` — per-strategy price bands gating real orders

Inspect:

```bash
psql kalshi_tennis
\dt
\d ticks
SELECT COUNT(*) FROM ticks;
```

Full schema details in `internal/store/AGENTS.md`.

## Tennis series tracked

12 core match-winner series:

| Ticker | Tour |
|---|---|
| `KXATPMATCH` | ATP main tour singles |
| `KXWTAMATCH` | WTA main tour singles |
| `KXITFMATCH` | ITF men singles |
| `KXITFWMATCH` | ITF women singles |
| `KXATPCHALLENGERMATCH` | ATP Challenger singles |
| `KXWTACHALLENGERMATCH` | WTA Challenger singles |
| `KXTENNISEXHIBITION` | Exhibition singles |
| `KXCHALLENGERMATCH` | Challenger (legacy) singles |
| `KXATPDOUBLES` | ATP main tour doubles |
| `KXWTADOUBLES` | WTA main tour doubles |
| `KXITFDOUBLES` | ITF men doubles |
| `KXITFWDOUBLES` | ITF women doubles |

Override with `series_tickers` in `app_config` DB table.

## Architecture

```
main.go                        entrypoint, signal handling, errgroup wiring
cmd/backtest/                  replay historical data through trading strategies
cmd/backfill-orders/           one-shot CLI: backfill stale real order status
internal/appconfig/            app.yaml / app.dev.yaml env config loading
internal/runtimeconfig/        app_config DB table CRUD (runtime tunables)
internal/config/               global Config = EnvConfig + RuntimeConfig (config.Cfg)
internal/kalshiAuth/           RSA-PSS-SHA256 request signing
internal/kalshiclient/         REST client (events, markets, pagination, rate limit)
internal/store/                PostgreSQL (GORM, single writer, batched inserts)
internal/ws/                   WebSocket manager (auto-reconnect, re-subscribe)
internal/scanner/              daily series scan, stores new events/markets
internal/tracker/              market subscription lifecycle (no per-match goroutine)
internal/scheduler/            schedules tracking at occurrence_datetime - lead
internal/reconciler/           resolves market results, settles orders
internal/schedulechecker/      refreshes stale occurrence_ts from REST
internal/orderbackfill/        refreshes stale real order status from REST
internal/backtest/             backtest engine, result persistence, cum P&L series
internal/apitennis/            API-Tennis WebSocket real-time scraper (primary score)
internal/kalshilivedata/       Kalshi live-data REST poller (backup score)
internal/algorithms/           pluggable trading strategies (match-point, order emission)
internal/strategies/           Build() factory wiring all strategies into MultiStrategyRuntime
internal/strategyconfig/       strategy_config table CRUD
internal/triggerranges/        trigger_ranges table CRUD
internal/liquiditypool/        liquidity pool (single source of truth for real cash)
internal/positions/            position lifecycle for sell-to-close pipeline
internal/signal/               close-timer strategy, simulated order emission
internal/pricebands/           fixed-band price analysis cron
internal/paperorderinsights/   paper order insights cron
internal/dashboardapi/         HTTP server (metrics, pprof, REST API for dashboard)
internal/dashboarddata/        live DB queries for dashboard
dashboard/                     SvelteKit + Vite dashboard
```

Concurrency: one goroutine each for WS manager, tick writer, scanner,
scheduler, reconciler, order backfill, schedule checker, API-Tennis scraper
(if enabled), Kalshi live-data poller (if enabled), MultiStrategyRuntime
timer, backtest recompute cron, pricebands cron, paperorderinsights cron,
dashboardapi HTTP server. One goroutine per scheduled match (waits until
start time, then subscribes). One goroutine per active match (if Kalshi
live-data enabled). All cancelled via root context on SIGINT/SIGTERM.

## Kalshi API docs

Local copies in [`docs/kalshi-api/`](docs/kalshi-api/) (`gs_*.md`, `ws_*.md`,
`openapi.yaml`). Design notes in [`docs/DESIGN.md`](docs/DESIGN.md).
Official docs at <https://docs.kalshi.com>.

## Diagnostic tools

```bash
# Backtest a strategy on historical data
go run ./cmd/backtest -strategy matchpoint
# Skip dead/illiquid markets (price < 0.05)
go run ./cmd/backtest -strategy matchpoint -min-price 0.05
# Debug mode: log strategy filter reasons (why signals were skipped)
go run ./cmd/backtest -strategy matchpoint -debug

# Backfill stale real order status (one-shot)
go run ./cmd/backfill-orders
```

## Notebooks

Analysis notebooks live in `research/`. They query the live PostgreSQL DB
read-only — never write to the DB from notebooks.

```bash
conda activate kalshi-ghost-trader
# Recreate from scratch:
conda env create -f environment.yml
```

Open in Zed or Jupyter:

```bash
zed research/nothing_happens.ipynb
```

## Snapshots (remote → local)

`scripts/snapshot.sh` exports gzipped JSON summaries + backtest output.
`scripts/fetch-snapshots.sh` rsyncs them locally to `snapshots/`.

```bash
# On remote — run manually (no cron currently configured)
ssh mint '/home/fahad/kalshi-ghost-trader/scripts/snapshot.sh'

# Locally — fetch snapshots
./scripts/fetch-snapshots.sh 192.168.1.246

# Inspect
zcat snapshots/<dir>/strategy_summary.json.gz | python3 -m json.tool
zcat snapshots/<dir>/backtest.txt.gz
```

Tiered retention (both ends): all snapshots within 48h, daily for 30 days,
weekly for 90 days, older deleted.

See [deploy/AGENTS.md](deploy/AGENTS.md) for remote setup instructions.

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```
