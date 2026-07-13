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

2. Copy env template:

   ```bash
   cp .env.example .env
   ```

3. Edit `.env`. At minimum set these three:

   ```
   KALSHI_API_KEY_ID=your-key-id
   KALSHI_PRIVATE_KEY_PATH=/absolute/path/to/private_key.pem
   KALSHI_ENV=demo
   ```

   Use `demo` first. Switch to `prod` once everything works.

4. Put your Kalshi RSA private key on disk at the path you set above.

## Run

```bash
go run ./cmd/ghost-trader
```

Logs go to stdout. Ctrl+C stops cleanly (flushes SQLite, unsubscribes WS).

## Configuration

All settings live in `.env`. Full reference in `.env.example`.

| Var | Default | What it does |
|---|---|---|
| `KALSHI_API_KEY_ID` | — | Kalshi API key ID (required) |
| `KALSHI_PRIVATE_KEY_PATH` | — | Path to RSA private key PEM (required) |
| `KALSHI_ENV` | `demo` | `demo` or `prod` |
| `DB_PATH` | `kalshi_tennis.db` | SQLite file path |
| `SERIES_TICKERS` | 8 tennis series | Comma-separated series to scan |
| `SCAN_INTERVAL_HOURS` | `24` | Hours between daily scans |
| `TRACK_LEAD_MINUTES` | `5` | Start WS this many minutes before match |
| `BATCH_SIZE` | `500` | SQLite batch insert size |
| `FLUSH_TIMEOUT_MS` | `250` | Max ms before partial batch flushes |
| `HTTP_TIMEOUT_SECS` | `30` | REST client timeout |
| `SCHEDULER_POLL_SECS` | `30` | How often scheduler checks DB |
| `WS_MIN_BACKOFF_SECS` | `1` | WS reconnect min backoff |
| `WS_MAX_BACKOFF_SECS` | `30` | WS reconnect max backoff |
| `METRICS_PORT` | `6060` | `0` disables metrics server |

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
- `scan_runs` — scan audit log

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
| `KWTACHALLENGERMATCH` | WTA Challenger |
| `KXTENNISEXHIBITION` | Exhibition |
| `KXCHALLENGERMATCH` | Challenger (legacy) |

Override with `SERIES_TICKERS` in `.env`.

## Architecture

```
cmd/ghost-trader/     entrypoint, signal handling, errgroup wiring
internal/config/      env var loading
internal/kalshiauth/  RSA-PSS-SHA256 request signing
internal/kalshiclient/  REST client (events, markets, pagination)
internal/store/       SQLite (WAL, single writer, batched inserts)
internal/ws/          WebSocket manager (auto-reconnect, re-subscribe)
internal/scanner/     daily series scan, stores new events/markets
internal/tracker/     per-match goroutine lifecycle
internal/scheduler/   schedules tracking at occurrence_datetime - lead
```

Concurrency: one goroutine each for WS manager, tick writer, scanner,
scheduler. One goroutine per tracked match. All cancelled via root context
on SIGINT/SIGTERM.

## Kalshi API docs

Local copies in repo root (`gs_*.md`, `ws_*.md`, `openapi.yaml`). Official
docs at <https://docs.kalshi.com>.

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```
