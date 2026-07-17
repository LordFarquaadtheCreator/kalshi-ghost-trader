# Kalshi Ghost Trader

Go service that tracks Kalshi tennis match markets in real-time over WebSocket
and stores every price, trade, and lifecycle message to SQLite for algorithm
testing.

## What it does

- Scans Kalshi daily for tennis match-winner markets (ATP, WTA, ITF, Challenger, exhibition)
- Schedules WebSocket tracking a few minutes before each match starts
- Streams ticker, trade, orderbook, and lifecycle events into SQLite
- Exposes runtime metrics + pprof on `127.0.0.1:6060`
- Ships a SvelteKit dashboard for live charts

## Requirements

- Go 1.22+ ([install](https://go.dev/doc/install))
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
   cp config.yaml.example config.yaml
   ```

3. Edit `config.yaml`. At minimum set these three:

   ```yaml
   api_key_id: "your-key-id"
   private_key_path: "/absolute/path/to/private_key.pem"
   environment: "demo"
   ```

   Use `demo` first. Switch to `prod` once everything works.

4. Put your Kalshi RSA private key on disk at the path you set above.

## Run

```bash
go run ./cmd/ghost-trader
```

Logs go to stdout. Ctrl+C stops cleanly (flushes SQLite, unsubscribes WS).

## Configuration

All settings live in `config.yaml`. Override path with `CONFIG_PATH` env var.
Full reference in `config.yaml.example`.

| Field | Default | What it does |
|---|---|---|
| `api_key_id` | — | Kalshi API key ID (required) |
| `private_key_path` | — | Path to RSA private key PEM (required) |
| `environment` | `demo` | `demo` or `prod` |
| `db_path` | `kalshi_tennis.db` | SQLite file path |
| `series_tickers` | 8 tennis series | List of series to scan |
| `scan_interval_hours` | `24` | Hours between daily scans |
| `track_lead_minutes` | `5` | Start WS this many minutes before match |
| `batch_size` | `500` | SQLite batch insert size |
| `flush_timeout_ms` | `250` | Max ms before partial batch flushes |
| `http_timeout_secs` | `30` | REST client timeout |
| `rate_limit_rps` | `15` | REST client max requests per second |
| `scheduler_poll_secs` | `30` | How often scheduler checks DB |
| `ws_min_backoff_secs` | `1` | WS reconnect min backoff |
| `ws_max_backoff_secs` | `30` | WS reconnect max backoff |
| `metrics_port` | `6060` | `0` disables metrics server |
| `apitennis_enabled` | `false` | Enable API-Tennis WebSocket scraper |
| `apitennis_api_key` | — | API-Tennis API key (required if enabled) |
| `apitennis_timezone` | `+00:00` | Timezone for API-Tennis requests |

## Dashboard

SvelteKit app showing live runtime charts.

```bash
cd dashboard
npm install
npm run dev
```

Opens at `http://localhost:5173`. Reads metrics from the Go service at
`http://127.0.0.1:6060`.

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
go build ./...
go vet ./...
```

Binary lands at `./ghost-trader`. Run it the same way:

```bash
./ghost-trader
```

## Deploy

Oracle Cloud ARM deployment scripts in `deploy/`. See
[deploy/README.md](deploy/README.md) for full instructions.

Quick version:

```bash
./deploy/build.sh                    # cross-compile ARM64 binary
./deploy/deploy.sh <instance-ip>     # upload + restart service
sudo journalctl -u ghost-trader -f   # tail logs on instance
```

## Database

SQLite with WAL mode. Single writer, batched inserts. Schema:

- `events` — tennis match events (1 per match)
- `markets` — 2 markets per event (one per player)
- `ticks` — every WS ticker/trade message with raw JSON
- `orderbook_events` — orderbook snapshots + deltas with raw JSON
- `lifecycle_events` — `market_lifecycle_v2` WS events
- `event_lifecycle_events` — `event_lifecycle` WS messages (event creation)
- `scan_runs` — scan audit log
- `orders` — simulated orders from strategy signals

Inspect:

```bash
sqlite3 kalshi_tennis.db
.tables
.schema ticks
SELECT COUNT(*) FROM ticks;
```

## Tennis series tracked

| Ticker | Tour |
|---|---|
| `KXATPMATCH` | ATP main tour |
| `KXWTAMATCH` | WTA main tour |
| `KXITFMATCH` | ITF men |
| `KXITFWMATCH` | ITF women |
| `KXATPCHALLENGERMATCH` | ATP Challenger |
| `KXWTACHALLENGERMATCH` | WTA Challenger |
| `KXTENNISEXHIBITION` | Exhibition |
| `KXCHALLENGERMATCH` | Challenger (legacy) |

Override with `series_tickers` in `config.yaml`.

## Architecture

```
cmd/ghost-trader/        entrypoint, signal handling, errgroup wiring
cmd/ghost-trader/metrics.go  runtime metrics + pprof HTTP handlers
cmd/validate/            config + connectivity validation tool
cmd/ws-debug/            WS + REST debug tool
cmd/backtest/           replay historical data through trading strategies
internal/config/         YAML config loading
internal/kalshiauth/     RSA-PSS-SHA256 request signing
internal/kalshiclient/   REST client (events, markets, pagination, rate limit)
internal/store/          SQLite (WAL, single writer, batched inserts)
internal/ws/             WebSocket manager (auto-reconnect, re-subscribe)
internal/scanner/        daily series scan, stores new events/markets
internal/tracker/        market subscription lifecycle (no per-match goroutine)
internal/scheduler/      schedules tracking at occurrence_datetime - lead
internal/apitennis/      API-Tennis WebSocket real-time scraper
internal/algorithms/      pluggable trading strategies (match-point detection, order emission)
internal/signal/          close-timer strategy, simulated order emission
```

Concurrency: one goroutine each for WS manager, tick writer, scanner,
scheduler, API-Tennis scraper (if enabled).
One goroutine per scheduled match (waits until start time, then subscribes).
All cancelled via root context on SIGINT/SIGTERM.

## Kalshi API docs

Local copies in [`docs/kalshi-api/`](docs/kalshi-api/) (`gs_*.md`, `ws_*.md`,
`openapi.yaml`). Design notes in [`docs/DESIGN.md`](docs/DESIGN.md).
Official docs at <https://docs.kalshi.com>.

## Diagnostic tools

```bash
# Validate config, credentials, REST/WS connectivity, DB
go run ./cmd/validate

# Debug WS handshake + REST signing
go run ./cmd/ws-debug

# Backtest a strategy on historical data
go run ./cmd/backtest -strategy matchpoint -db kalshi_tennis.db
# Skip dead/illiquid markets (price < 0.05)
go run ./cmd/backtest -strategy matchpoint -db kalshi_tennis.db -min-price 0.05
# Debug mode: log strategy filter reasons (why signals were skipped)
go run ./cmd/backtest -strategy matchpoint -debug
```

## Notebooks

Analysis notebooks live in `notebooks/`. They query the live SQLite DB
read-only — never write to the DB from notebooks.

```bash
conda activate kalshi-ghost-trader
# Recreate from scratch:
conda env create -f environment.yml
```

Open in Zed or Jupyter:

```bash
zed notebooks/nothing_happens.ipynb
```

## Snapshots (remote → local)

Since the DB lives on the remote instance, `scripts/snapshot.sh` runs on remote
via cron and exports gzipped JSON summaries + backtest output. Fetch with
`scripts/fetch-snapshots.sh`.

```bash
# On remote — add to crontab (every 6 hours)
0 */6 * * * /data/snapshot.sh >> /data/snapshots/cron.log 2>&1

# Locally — fetch snapshots
./scripts/fetch-snapshots.sh <instance-ip>

# Inspect
zcat snapshots/<dir>/strategy_summary.json.gz | python3 -m json.tool
zcat snapshots/<dir>/backtest.txt.gz
```

Tiered retention (both ends): all snapshots within 48h, daily for 30 days,
weekly for 90 days, older deleted.

See [deploy/README.md](deploy/README.md) for remote setup instructions.

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```
