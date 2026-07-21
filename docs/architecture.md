# System Architecture

## Overview

Go service tracking Kalshi tennis match markets in real-time via WebSocket,
storing every price/trade/lifecycle message to SQLite for algorithm testing.
Optional API-Tennis WebSocket provides point-by-point score data. SvelteKit
dashboard for monitoring.

```
                          ┌──────────────────────────────────────────┐
                          │        Linux Mint box (LAN, 24/7)         │
                          │                                          │
  Kalshi ──WS/Signal──▶   │  ┌──────────┐    ┌───────────────┐       │
    WebSocket             │  │ Go Ghost │    │ SvelteKit     │       │
                          │  │ Trader   │◄──►│ Dashboard     │       │
  API-Tennis ──WS──▶      │  │ (ticks)  │    │ (:5173)       │       │
    Real-Time             │  │ (:6060)  │    └───────────────┘       │
                          │  └─────┬────┘                             │
                          │        │                                  │
                          │  ┌─────▼──────────┐                       │
                          │  │   SQLite (WAL)  │                       │
                          │  │  31GB+ and grow │                       │
                          │  └────────────────┘                       │
                          └──────────────────────────────────────────┘
```

## Processes

| Process | Language | Port | Role |
|---|---|---|---|
| `ghost-trader` | Go | 6060 | WebSocket feeds, REST scans, tick storage, API-Tennis scraper, strategy execution, pprof/metrics |
| `dashboard` | JS (SvelteKit) | 5173 | Tracked matches, orders, strategies, system metrics, charts |

Both managed by systemd. Dashboard `BindsTo` backend.

## Package Layout

```
main.go              — entrypoint, signal handling, errgroup wiring
cmd/
  backtest/          — replay historical data through trading strategies
  pricebands/        — price band analysis across all strategies
internal/
  config/            — YAML config loading (legacy, superseded by app_config table)
  appconfig/         — runtime config from app_config DB table
  kalshiAuth/        — RSA-PSS-SHA256 request signing (PKCS#8 + PKCS#1)
  kalshiclient/      — REST client (events, markets, pagination, rate limit)
  store/             — SQLite (WAL, single writer, batched inserts, app_config, orders, liquidity_pool)
  ws/                — WebSocket manager (auto-reconnect, re-subscribe, dispatch)
  scanner/           — daily series scan, stores new events/markets
  tracker/           — market subscription lifecycle (no per-match goroutine)
  scheduler/         — schedules tracking at occurrence_datetime - lead
  reconciler/        — resolves market results, settles orders
  schedulechecker/   — validates scheduled match tracking
  backtest/          — backtest engine, result cache (5min TTL), price band analysis
  apitennis/         — API-Tennis WebSocket real-time scraper (primary score source)
  kalshilivedata/    — Kalshi live-data REST poller (backup score source)
  algorithms/        — pluggable trading strategies + Markov model + order sizing
  signal/            — close-timer strategy, simulated order emission
dashboard/           — SvelteKit + Vite frontend
```

`cmd/` contains commands ONLY — executable entrypoints, no library code.

## Concurrency Model

- One WSManager goroutine: owns connection, read loop, dispatch
- One TickWriter goroutine: batches inserts, single SQLite writer
- One Scanner goroutine: daily REST scan
- One Scheduler goroutine: polls DB, schedules match tracking
- One API-Tennis goroutine (if enabled): WS read loop, per-match dispatch
- One goroutine per active match (if Kalshi live-data enabled): REST poll loop
- One goroutine per scheduled match: waits until start time, then subscribes

## Config

Two-layer config:
- **`app.yaml` / `app.dev.yaml`** — technical config (environment, credentials, paths). See `internal/appconfig/`.
- **`app_config` DB table** — runtime tunables (intervals, strategy params, bankroll). Dashboard-editable.

`app_config`, `liquidity_pool`, `strategy_config` seeded automatically by SQL migrations on first startup.

## Hosting

Scraper runs on Linux Mint box on LAN — not a cloud instance.

```
ssh mint                                    # alias in ~/.ssh/config
ssh fahad@192.168.1.246                     # direct
```

- Host: `linux-mint`, Linux Mint 24.04, x86_64
- Key: `~/.ssh/id_ed25519` (copied via `ssh-copy-id`)
- Passwordless sudo for `systemctl` + `journalctl` (via `/etc/sudoers.d/fahad-systemctl`)
- Repo: `/home/fahad/kalshi-ghost-trader`
- DB: PostgreSQL `kalshi_tennis` on `127.0.0.1:5432`

### systemd services

- `kalshi-ghost-trader.service` — backend binary, `Restart=always`
- `kalshi-dashboard.service` — Vite dev server, `BindsTo` backend

Unit files: `/etc/systemd/system/kalshi-{ghost-trader,dashboard}.service`

```bash
ssh mint 'sudo -n systemctl status kalshi-ghost-trader --no-pager -n 20'
ssh mint 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'
ssh mint 'sudo -n systemctl restart kalshi-ghost-trader kalshi-dashboard'
```

### Update workflow

```bash
./deploy/deploy.sh mint main
```

Builds locally, scp artifacts, syncs service file, restarts. If schema changed, run migration first.

## Monitoring

```bash
# Runtime metrics + pprof (built into app, port 6060)
curl http://127.0.0.1:6060/metrics
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30

# External resource monitor (CPU, RSS, network IO, Go runtime)
./scripts/monitor.sh $(pgrep -f ghost-trader) 2 metrics.csv
```

## Snapshots

`scripts/snapshot.sh` runs on mint via cron, exports gzipped JSON summaries + backtest output
to `snapshots/YYYYMMDD_HHMM/`. `scripts/fetch-snapshots.sh` rsyncs them locally.

Exports: orders, unresolved orders, events summary, strategy summary, tick stats,
scan runs, lifecycle summary, points summary, db stats, backtest output, meta.

Tiered retention (both remote + local):
- 0–48h: keep all (8 at 6h intervals)
- 2–30 days: keep 1 per day
- 30–90 days: keep 1 per week
- 90+ days: delete

Cron: `0 */6 * * *` on mint.

## Backup

On mint — daily full DB backup, keep 7 days:
```bash
ssh mint 'pg_dump -U kalshi kalshi_tennis | gzip > /home/fahad/kalshi-ghost-trader/backups/kalshi_$(date +%Y%m%d).sql.gz'
ssh mint 'find /home/fahad/kalshi-ghost-trader/backups/ -name "kalshi_*.sql.gz" -mtime +7 -delete'
```

## Dashboard

SvelteKit 2 + Svelte 5 (runes) + Vite + Chart.js. Plain CSS design system (no Tailwind).

| Route | Description |
|---|---|
| `/matches` | Tracked matches table, links to match detail |
| `/matches/[event_ticker]` | Match detail: price chart, market cards, sim orders |
| `/orders` | Paper orders: open positions + settled trades, filters, summary |
| `/strategies` | Simulated outcomes: backtest results, charts, per-strategy order tables |
| `/system` | System metrics: Go runtime stats, memory/GC charts |

## Tennis Series

12 core match-winner series:
- KXATPMATCH, KXWTAMATCH (main tour singles)
- KXITFMATCH, KXITFWMATCH (ITF singles)
- KXATPCHALLENGERMATCH, KXWTACHALLENGERMATCH (Challenger singles)
- KXTENNISEXHIBITION, KXCHALLENGERMATCH (exhibition/legacy singles)
- KXATPDOUBLES, KXWTADOUBLES (main tour doubles)
- KXITFDOUBLES, KXITFWDOUBLES (ITF doubles)

## Python (notebooks)

Conda env for analysis notebooks in `research/`. Notebooks query the live SQLite DB read-only.

```bash
conda activate kalshi-ghost-trader
conda env create -f environment.yml  # recreate from scratch
```
