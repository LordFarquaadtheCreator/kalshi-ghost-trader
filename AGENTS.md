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
cp .env.example .env
# Edit .env: set KALSHI_API_KEY_ID, KALSHI_PRIVATE_KEY_PATH, KALSHI_ENV
go run ./cmd/ghost-trader
```

## Architecture

Each package has its own `AGENTS.md` with package-specific gotchas.

- `cmd/ghost-trader/` — entrypoint, signal handling, errgroup wiring
- `internal/config/` — env var loading
- `internal/kalshiauth/` — RSA-PSS-SHA256 request signing (PKCS#8 + PKCS#1)
- `internal/kalshiclient/` — REST client (events, markets, pagination)
- `internal/store/` — SQLite (WAL, single writer, batched tick inserts)
- `internal/ws/` — WebSocket manager (auto-reconnect, re-subscribe, dispatch)
- `internal/scanner/` — daily series scan, stores new events/markets
- `internal/tracker/` — per-match goroutine lifecycle (independent cancellation)
- `internal/scheduler/` — schedules tracking at occurrence_datetime - lead

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single SQLite writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One goroutine per match: consumes per-match channel (independent ctx)

## SQLite Schema

- `events` — tennis match events (1 per match)
- `markets` — 2 markets per event (one per player)
- `ticks` — every WS message (ticker, trade, orderbook) with raw JSON
- `lifecycle_events` — market_lifecycle_v2 WS events
- `scan_runs` — scan audit log

## Tennis Series

8 core match-winner series:
- KXATPMATCH, KXWTAMATCH (main tour)
- KXITFMATCH, KXITFWMATCH (ITF)
- KXATPCHALLENGERMATCH, KXWTACHALLENGERMATCH (Challenger)
- KXTENNISEXHIBITION, KXCHALLENGERMATCH (exhibition/legacy)

## Verification

```bash
go build ./...   # compiles
go vet ./...     # no issues
```
