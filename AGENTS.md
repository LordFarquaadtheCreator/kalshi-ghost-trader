# Kalshi Ghost Trader

Go service tracking Kalshi tennis match markets in real-time via WebSocket,
storing every price/trade/lifecycle message to SQLite for algorithm testing.

## Build

```bash
go build ./...
go vet ./...
```

## Run

```bash
cp config.yaml.example config.yaml
# Edit config.yaml: set api_key_id, private_key_path, environment
go run ./cmd/ghost-trader
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

- `cmd/ghost-trader/` — entrypoint, signal handling, errgroup wiring
- `cmd/validate/` — config + connectivity validation tool
- `cmd/ws-debug/` — WS + REST debug tool
- `cmd/backtest/` — replay historical data through trading strategies
- `internal/config/` — YAML config loading
- `internal/kalshiauth/` — RSA-PSS-SHA256 request signing (PKCS#8 + PKCS#1)
- `internal/kalshiclient/` — REST client (events, markets, pagination, rate limit)
- `internal/store/` — SQLite (WAL, single writer, batched tick inserts)
- `internal/ws/` — WebSocket manager (auto-reconnect, re-subscribe, dispatch)
- `internal/scanner/` — daily series scan, stores new events/markets
- `internal/tracker/` — market subscription lifecycle (no per-match goroutine)
- `internal/scheduler/` — schedules tracking at occurrence_datetime - lead
- `internal/apitennis/` — API-Tennis WebSocket real-time scraper (optional)
- `internal/algorithms/` — pluggable trading strategies (match-point detection, order emission)
- `internal/signal/` — close-timer strategy, simulated order emission

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single SQLite writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One API-Tennis goroutine (if enabled): WS read loop, per-match dispatch
- One goroutine per scheduled match: waits until start time, then subscribes

## SQLite Schema

- `events` — tennis match events (1 per match). `coverage` tag set at settlement.
- `markets` — 2 markets per event (one per player). FK to events, triggers handle cascade.
- `ticks` — every WS message (ticker, trade) with raw JSON payload. Log table, no FK.
- `orderbook_events` — orderbook snapshots + deltas with raw JSON payload.
- `lifecycle_events` — market_lifecycle_v2 WS events.
- `event_lifecycle_events` — event_lifecycle WS messages (event creation announcements).
- `scan_runs` — scan audit log.

Cascade deletes use flattened triggers (not recursive FK chains):
- `trg_markets_delete_cascade` — cleans ticks, orderbook, lifecycle on market delete.
- `trg_events_delete_cascade` — cleans markets, event_lifecycle, orders on event delete.

Coverage classification on events at settlement:
- `full` — ≥100 ticks spanning ≥290s in final 5-min pre-close window.
- `low_freq` — 1-99 ticks in that window.
- `none` — no ticks (auto-pruned on settlement by P6).

Payload retention: non-`full` events have `payload` NULLed in ticks/orderbook at settlement (P7).
Orphan janitor (`CleanOrphans`) and late-parenting sweep (`AdoptOrphans`) run after each scan cycle.

## Remote Deployment

App runs on Oracle Cloud ARM instance. DB is on remote disk — not accessible locally.
See `deploy/README.md` for deployment instructions.

## Snapshots (Remote → Local)

Since DB lives on remote, `scripts/snapshot.sh` runs on remote via cron and exports
gzipped JSON summaries + backtest output to `/data/snapshots/YYYYMMDD_HHMM/`.
`scripts/fetch-snapshots.sh` rsyncs them locally to `snapshots/`.

Exports:
- `orders.json.gz` — all simulated orders with computed P&L
- `orders_unresolved.json.gz` — orders without market result yet
- `events_summary.json.gz` — events with coverage, market status, tick counts
- `strategy_summary.json.gz` — per-strategy aggregates (win rate, ROI, P&L)
- `tick_stats.json.gz` — tick counts per market (top 500)
- `scan_runs.json.gz` — recent scan audit log
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

Cron (on remote, every 6 hours):
```
0 */6 * * * /data/snapshot.sh >> /data/snapshots/cron.log 2>&1
```

Fetch locally:
```bash
./scripts/fetch-snapshots.sh <instance-ip>
```

Inspect:
```bash
zcat snapshots/<dir>/strategy_summary.json.gz | python3 -m json.tool
zcat snapshots/<dir>/backtest.txt.gz
```

## Backup

On remote — daily full DB backup, keep 7 days:
```bash
sqlite3 /data/kalshi_tennis.db ".backup /data/backups/kalshi_$(date +%Y%m%d).db"
find /data/backups/ -name "kalshi_*.db" -mtime +7 -delete
```

Locally — atomic snapshot while scraper running:
```bash
mkdir -p backups
sqlite3 kalshi_tennis.db ".backup backups/kalshi_tennis_$(date +%Y%m%d_%H%M).db"
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

Conda env for analysis notebooks in `notebooks/`.

```bash
conda activate kalshi-ghost-trader
# Recreate from scratch:
conda env create -f environment.yml
```

Notebooks query the live SQLite DB read-only. Never open the DB for writes from notebooks.

## Backtest

Replay historical tick data through a strategy and report P&L.

```bash
go run ./cmd/backtest -strategy matchpoint -db kalshi_tennis.db
go run ./cmd/backtest -strategy matchpoint -debug   # log filter reasons
```

Strategies register in `strategies` map in `cmd/backtest/main.go`.
Must implement `replayStrategy` (Strategy + `SetReplayTime` + `OnPriceAt`).

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```

## Simulated Trades

Simulated trades **always** run all strategies. No strategy skipping, filtering, or conditional activation.
Every strategy registered in the system participates in every match — no exceptions.
This ensures complete paper-trail data for comparison and backtesting.
