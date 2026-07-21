# Kalshi Ghost Trader

Go service tracking Kalshi tennis match markets in real-time via WebSocket,
storing every price/trade/lifecycle message to SQLite for algorithm testing.

## Build

```bash
go build ./...
go vet ./...
```

## Run

Two-layer config:
- **`app.yaml` / `app.dev.yaml`** — technical config (environment, credentials, paths). See `internal/appconfig/`.
- **`app_config` DB table** — runtime tunables (intervals, strategy params, bankroll). Dashboard-editable.

```bash
# First-time setup:
cp app.dev.yaml.example app.dev.yaml   # dev (demo keys)
# OR
cp app.yaml.example app.yaml           # prod (real keys)
# Edit: set kalshi_api_key_id, kalshi_private_key_path, environment

# app_config, liquidity_pool, strategy_config seeded automatically
# by SQL migrations on first startup. No manual seeding needed.

# Run (dev — auto-selects app.dev.yaml if present):
go run ./cmd/ghost-trader

# Run (prod — explicit):
APP_ENV=prod go run ./cmd/ghost-trader
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
- `cmd/pricebands/` — price band analysis across all strategies (per-day + aggregate)
- `cmd/backfill/` — backfill historical data
- `cmd/test-order/` — manual test order CLI tool (single IOC bid to Kalshi)
- `internal/config/` — YAML config loading (legacy, superseded by app_config table)
- `internal/kalshiAuth/` — RSA-PSS-SHA256 request signing (PKCS#8 + PKCS#1)
- `internal/kalshiclient/` — REST client (events, markets, pagination, rate limit)
- `internal/store/` — SQLite (WAL, single writer, batched tick inserts, app_config, orders, liquidity_pool)
- `internal/ws/` — WebSocket manager (auto-reconnect, re-subscribe, dispatch)
- `internal/scanner/` — daily series scan, stores new events/markets
- `internal/tracker/` — market subscription lifecycle (no per-match goroutine)
- `internal/scheduler/` — schedules tracking at occurrence_datetime - lead
- `internal/reconciler/` — resolves market results, settles orders
- `internal/schedulechecker/` — validates scheduled match tracking
- `internal/backtest/` — backtest engine, result cache (5min TTL), price band analysis
- `internal/apitennis/` — API-Tennis WebSocket real-time scraper (optional, primary score source)
- `internal/kalshilivedata/` — Kalshi live-data REST poller (optional, backup score source)
- `internal/algorithms/` — pluggable trading strategies (match-point detection, order emission)
- `internal/signal/` — close-timer strategy, simulated order emission
- `dashboard/` — SvelteKit + Vite dashboard (real orders, liquidity pool, config management, charts)

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single SQLite writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One API-Tennis goroutine (if enabled): WS read loop, per-match dispatch
- One goroutine per active match (if Kalshi live-data enabled): REST poll loop
- One goroutine per scheduled match: waits until start time, then subscribes

## SQLite Schema

- `events` — tennis match events (1 per match). `coverage` tag set at settlement.
- `markets` — 2 markets per event (one per player). FK to events, triggers handle cascade.
- `ticks` — every WS message (ticker, trade) with raw JSON payload. Log table, no FK.
- `orderbook_events` — orderbook snapshots + deltas with raw JSON payload.
- `lifecycle_events` — market_lifecycle_v2 WS events.
- `event_lifecycle_events` — event_lifecycle WS messages (event creation announcements).
- `kalshi_scores` — live score snapshots from Kalshi /live_data (backup score source).
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

## Deployment (Linux Mint box)

Scraper runs on Linux Mint box on LAN — same machine as `ssh mint` (see below).
Not a cloud instance. DB lives on local disk there (31GB+).

```bash
ssh mint                                    # alias in ~/.ssh/config
ssh fahad@192.168.1.246                     # direct
```

Key: `~/.ssh/id_ed25519` (copied via `ssh-copy-id`). Host: `linux-mint`,
Linux Mint 24.04, x86_64. Passwordless sudo granted for `systemctl` + `journalctl`
(via `/etc/sudoers.d/fahad-systemctl`).

### systemd services

- `kalshi-ghost-trader.service` — backend binary, `Restart=always`
- `kalshi-dashboard.service` — Vite dev server, `BindsTo` backend

Unit files: `/etc/systemd/system/kalshi-{ghost-trader,dashboard}.service`
Repo: `/home/fahad/kalshi-ghost-trader`
DB: `/home/fahad/kalshi-ghost-trader/kalshi_tennis.db`
Ports: backend `6060` (all interfaces), dashboard `5173` (all interfaces)

```bash
ssh mint 'sudo -n systemctl status kalshi-ghost-trader --no-pager -n 20'
ssh mint 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'
ssh mint 'sudo -n systemctl restart kalshi-ghost-trader kalshi-dashboard'
```

### Mint deploy tweaks (uncommitted, stashed on pull)

Mint has local-only changes never committed — stashed before `git pull`, popped after:
- `cmd/ghost-trader/main.go` — metrics binds `0.0.0.0` (not `127.0.0.1`)
- `dashboard/src/lib/api.js` — empty API URLs (uses Vite proxy, not localhost)
- `dashboard/vite.config.js` — `server.host=0.0.0.0`, proxy `/api`+`/metrics`+`/debug` to `127.0.0.1:6060`

### Update workflow

```bash
ssh mint 'cd /home/fahad/kalshi-ghost-trader && \
  git stash push -u -m "mint-deploy-tweaks" && \
  git pull --ff-only origin main && \
  git stash pop && \
  go build -o ghost-trader ./cmd/ghost-trader && \
  sudo -n systemctl restart kalshi-ghost-trader && sleep 2 && \
  sudo -n systemctl restart kalshi-dashboard'
```

If schema changed, run migration first.

## Snapshots (Remote → Local)

`scripts/snapshot.sh` runs on mint via cron, exports gzipped JSON summaries + backtest output
to `snapshots/YYYYMMDD_HHMM/`. `scripts/fetch-snapshots.sh` rsyncs them locally to `snapshots/`.

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

Cron (on mint, every 6 hours):
```
0 */6 * * * /home/fahad/kalshi-ghost-trader/scripts/snapshot.sh >> /home/fahad/kalshi-ghost-trader/snapshots/cron.log 2>&1
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

On mint — daily full DB backup, keep 7 days:
```bash
ssh mint 'sqlite3 /home/fahad/kalshi-ghost-trader/kalshi_tennis.db ".backup /home/fahad/kalshi-ghost-trader/backups/kalshi_$(date +%Y%m%d).db"'
ssh mint 'find /home/fahad/kalshi-ghost-trader/backups/ -name "kalshi_*.db" -mtime +7 -delete'
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

## Price Band Analysis

Run all strategies, bucket orders into fixed price bands, output per-day +
aggregate tables to `pricebands_output.txt`.

```bash
go run ./cmd/pricebands -db kalshi_tennis.db
# Filter to single day:
go run ./cmd/pricebands -db kalshi_tennis.db -day 2026-07-17
# Custom output path:
go run ./cmd/pricebands -db kalshi_tennis.db -out /tmp/bands.txt
```

Outputs 4 sections per day: per-strategy-per-band, cross-strategy band totals,
best bands (N≥5, WR≥55%), and a cross-day tier-1 summary excluding
fadelongshot*/nofade. Run on mint when DB is large.

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
