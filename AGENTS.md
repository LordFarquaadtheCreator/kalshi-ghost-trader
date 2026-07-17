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
- `internal/flashscore/` — FlashScore point-by-point scraper (optional)
- `internal/apitennis/` — API-Tennis WebSocket real-time point-by-point scraper (optional)
- `internal/algorithms/` — pluggable trading strategies (match-point detection, order emission, quota guard, real order emitter)
- `internal/signal/` — close-timer strategy, simulated order emission

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single SQLite writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One FlashScore goroutine (if enabled): scan loop + point poll loop
- One API-Tennis goroutine (if enabled): WS read loop, per-match dispatch
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

Replay historical point + tick data through a strategy and report P&L.

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

## Order Emission Pipeline

```
strategies → paperGuard → paperEmitter (TickWriter, ALWAYS writes to DB)
                 ↓ (inner, if paper quota approved)
              realGuard → KalshiOrderEmitter (if real_trading_enabled)
                 ↓ (if real quota approved)
              NoopEmitter (if real_trading disabled)
```

Two independent `QuotaGuard` instances:
- **Paper guard** — always active, tracks `paper_budget_total` / `paper_budget_floor`
- **Real guard** — only when `real_trading_enabled: true`, tracks `real_budget_total` / `real_budget_floor`

Both have independent cooldowns, rate limits, daily quotas. Paper trail always complete regardless.

### QuotaGuard (4 layers)

1. **Per-market cooldown** — first order per market passes, rest dropped within window (default 30s). Prevents N strategies firing N orders on same market.
2. **Budget floor** — tracks cumulative spend locally via `atomic.Int64` (cents). If remaining would drop below floor, order dropped and spend rolled back. No REST balance query.
3. **Global rate limit** — token bucket, non-blocking. Drops if no token (never blocks WS goroutine). Default 2 orders/sec.
4. **Daily quota** — hard atomic counter ceiling. Resettable via `ResetDailyQuota()`.

### KalshiOrderEmitter (real orders)

- Submits IOC bid orders to `POST /portfolio/events/orders` (V2)
- Hard contract cap (`real_max_contracts`, default 50)
- Per-order HTTP timeout (`real_order_timeout_secs`, default 10s)
- `taker_at_cross` self-trade prevention
- All submissions logged with order_id, fill_count, remaining_count
- Errors logged, not propagated — strategy goroutines never block on REST failures

### Config

```yaml
order_quota_enabled: true          # quota guard active
order_quota_cooldown_secs: 30     # per-market cooldown
order_quota_max_per_sec: 2         # global rate limit
order_quota_daily_limit: 100       # hard daily ceiling

paper_budget_total: 1000.00       # paper trading budget
paper_budget_floor: 50.00         # paper floor

real_trading_enabled: false       # LIVE orders off by default
real_max_contracts: 50            # hard cap per order
real_order_timeout_secs: 10       # per-order HTTP timeout

real_budget_total: 100.00         # real trading budget
real_budget_floor: 5.00           # real floor
```

### Going live checklist

1. Test in demo first: `environment: demo` + `real_trading_enabled: true`
2. Start with small `real_budget_total` (e.g. $10)
3. Monitor `remaining_budget` in logs — every approved order logs it
4. `real_max_contracts` hard cap — can't be exceeded even if strategy suggests more
5. IOC orders — no unfilled orders resting on book
6. Watch for `REAL TRADING ENABLED` warning on startup

## Simulated Trades

Simulated trades **always** run all strategies. No strategy skipping, filtering, or conditional activation.
Every strategy registered in the system participates in every match — no exceptions.
This ensures complete paper-trail data for comparison and backtesting.
