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
- `internal/config/` — YAML config loading
- `internal/kalshiauth/` — RSA-PSS-SHA256 request signing (PKCS#8 + PKCS#1)
- `internal/kalshiclient/` — REST client (events, markets, pagination, rate limit)
- `internal/store/` — SQLite (WAL, single writer, batched tick inserts)
- `internal/ws/` — WebSocket manager (auto-reconnect, re-subscribe, dispatch)
- `internal/scanner/` — daily series scan, stores new events/markets
- `internal/tracker/` — market subscription lifecycle (no per-match goroutine)
- `internal/scheduler/` — schedules tracking at occurrence_datetime - lead
- `internal/flashscore/` — FlashScore point-by-point scraper (optional)

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single SQLite writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One FlashScore goroutine (if enabled): scan loop + point poll loop
- One goroutine per scheduled match: waits until start time, then subscribes

## SQLite Schema

- `events` — tennis match events (1 per match). `coverage` tag set at settlement.
- `markets` — 2 markets per event (one per player). FK to events, triggers handle cascade.
- `ticks` — every WS message (ticker, trade) with raw JSON payload. Log table, no FK.
- `orderbook_events` — orderbook snapshots + deltas with raw JSON payload.
- `lifecycle_events` — market_lifecycle_v2 WS events.
- `event_lifecycle_events` — event_lifecycle WS messages (event creation announcements).
- `flashscore_matches` — FlashScore match mapping (fs_match_id → event_ticker).
- `points` — point-by-point tennis score data from FlashScore.
- `scan_runs` — scan audit log.

Cascade deletes use flattened triggers (not recursive FK chains):
- `trg_markets_delete_cascade` — cleans ticks, orderbook, lifecycle on market delete.
- `trg_events_delete_cascade` — cleans markets, event_lifecycle, points, flashscore on event delete.

Coverage classification on events at settlement:
- `full` — ≥100 ticks spanning ≥290s in final 5-min pre-close window.
- `low_freq` — 1-99 ticks in that window.
- `points_only` — no ticks but has FlashScore score data.
- `none` — no ticks and no points (auto-pruned on settlement by P6).

Payload retention: non-`full` events have `payload` NULLed in ticks/orderbook at settlement (P7).
Orphan janitor (`CleanOrphans`) and late-parenting sweep (`AdoptOrphans`) run after each scan cycle.

## Backup

```bash
# Create a snapshot of the live DB
mkdir -p backups
sqlite3 kalshi_tennis.db ".backup backups/kalshi_tennis_$(date +%Y%m%d_%H%M).db"
```

Backups live in `backups/`. The `.backup` command produces an atomic, transactionally-consistent
copy even while the scraper is writing.

## Tennis Series

8 core match-winner series:
- KXATPMATCH, KXWTAMATCH (main tour)
- KXITFMATCH, KXITFWMATCH (ITF)
- KXATPCHALLENGERMATCH, KXWTACHALLENGERMATCH (Challenger)
- KXTENNISEXHIBITION, KXCHALLENGERMATCH (exhibition/legacy)

## Python (notebooks)

Conda env for analysis notebooks in `notebooks/`.

```bash
conda activate kalshi-ghost-trader
# Recreate from scratch:
conda env create -f environment.yml
```

Notebooks query the live SQLite DB read-only. Never open the DB for writes from notebooks.

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```
