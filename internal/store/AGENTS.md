# internal/store

PostgreSQL layer. Single-writer architecture via TickWriter.

## Files

- `store.go` — DB struct, New, Close, migrate, nowMillis helper
- `schema.go` — schemaDDL constant (full DDL for all tables + cascade triggers)
- `types.go` — Event, Market, Tick, LifecycleEvent, EventLifecycleEvent, OrderbookEvent, Order
- `events.go` — UpsertEvent, UpsertEventCheckNew, DeleteEvent, EventExists, GetSeriesTicker, GetEventTitle, SetCoverage, DropOrphanPayloads, GetCoverage, GetAllEventsForMatching
- `markets.go` — UpsertMarket, UpsertMarketCheckNew, GetMarket, GetActiveMarkets (status IN open/active/determined — keeps tracking through determination until settled), GetUnresolvedMarkets (3 clauses: empty result + has orders, past close+grace, OR result set + has non-terminal real orders), GetMarketsByEvent, scanMarket/scanMarketRow helpers
- `ticks.go` — InsertTickBatch
- `orderbook.go` — InsertOrderbookBatch
- `lifecycle.go` — InsertLifecycleEvent, InsertEventLifecycleEvent, ApplyLifecycleEvent
- `scan.go` — RecordScanRun
- `janitor.go` — CleanOrphans, AdoptOrphans
- `tickwriter.go` — TickWriter goroutine (batched writes across 4 channels)
- `appconfig.go` — app_config KV store, app_config_history (change tracking), liquidity_pool, strategy_config, trigger_ranges
- `backtest.go` — SaveBacktestResult, GetAllBacktestResults, GetBacktestRunTS, GetLastFinalizedSettlementTS
- `pricebands.go` — GetComputedDays, SavePriceBandDay, GetAllPriceBandResults, GetPriceBandRunTS
- `simulation.go` — GetComputedInsightDays, SaveSimulationInsightDay, GetAllSimulationInsights, GetSimulationInsightRunTS
- `migrations.go` — embedded SQL migration runner (files in `migrations/*.sql`, applied in order)

## Connection settings

Foreign keys enabled. Cascade deletes via PL/pgSQL trigger functions (see `store.go` Migrate).

## Connection pool

MaxOpenConns=10, MaxIdleConns=5. Single writer via TickWriter goroutine.

## Tables

- `events` — tennis match events. PK: event_ticker.
- `markets` — 2 per event (one per player). PK: market_ticker. FK: event_ticker.
- `ticks` — every WS message (ticker, trade). No FK to markets. Log table — never reject. Extracted hot fields + raw JSON payload.
- `orderbook_events` — orderbook snapshots + deltas. No FK. Same reason. Delta: price/delta/side extracted. Snapshot: full levels in payload.
- `lifecycle_events` — market_lifecycle_v2 WS events. No FK. Same reason.
- `event_lifecycle_events` — event_lifecycle WS messages (event creation). No FK.
- `points` — point-by-point score data from API-Tennis. No FK (may arrive before event stored).
- `kalshi_scores` — live score snapshots from Kalshi /live_data (backup source). PK: event_ticker.
- `orders` — simulated + real orders from strategy signals. No FK. Traceable via match_ticker + market_ticker. Includes `match_title` and `player_name` columns (populated by real emitter, empty for legacy/paper rows).
- `scan_runs` — scan audit log.

## Why no FK on ticks/orderbook_events/lifecycle_events/event_lifecycle_events

WS messages can arrive before scanner stores the market. FK would reject the tick. Data loss. Log tables must never reject.

## TickWriter

Single goroutine. Batches inserts. Four channels: `in` (ticks, 8192 buffer), `orderbookIn` (orderbook events, 8192 buffer), `lifecycleIn` (lifecycle, 1024 buffer), `eventLifecycleIn` (event lifecycle, 1024 buffer). Non-blocking ingest — drops on full buffer with warning.

Flush triggers: batch full, timer fires, lifecycle event arrives, ctx cancelled.

After inserting a lifecycle event, calls `ApplyLifecycleEvent` to update `markets` table status. Maps: activated→active (also updates open_ts if present), deactivated→inactive, determined→determined (updates result+settlement_ts), settled→finalized (updates result+settlement_ts), close_date_updated→close_ts only. Each type only updates its own columns — preserves close_ts/settlement_ts from other sources. Implicit transitions (initialized→active, active→closed) emit no WS event — rely on REST scan.

On `settled`: after both markets in an event are finalized, classifies coverage (`full`/`low_freq`/`none`). If `none`, prunes the event entirely. If not `full`, drops raw payloads from ticks/orderbook (saves disk space).

## Upsert pattern

`INSERT OR IGNORE` + check `RowsAffected()`. If 0, row existed — run UPDATE. `ON CONFLICT DO UPDATE` returns 1 for both insert and update, can't distinguish new vs existing.

## Migrations

SQL migrations live in `migrations/*.sql`, embedded via `go:embed`. `RunAllMigrations()` applies unapplied files in sorted order on startup.

- **Changing default/seed data** — add a new numbered `.sql` file (e.g. `0003_*.sql`). Use `INSERT OR IGNORE` to avoid overwriting existing values.
- **Schema changes** — prefer GORM `AutoMigrate` (add struct to `allModels` in `schema.go`). Use SQL migrations for indexes, triggers, or data seeds.
- Migrations are idempotent and ordered. Never edit an applied migration — add a new one.

## Transactions

Multi-step writes **must** use transactions. Wrap in `db.Transaction(func(tx *gorm.DB) error { ... })`.

- Read-then-write patterns (e.g. read old value, write new, insert history row) require transactions for atomicity.
- Batch writes that must succeed or fail together require transactions.
- Single-row upserts via `ON CONFLICT` don't need explicit transactions (GORM wraps them).

## Gotchas

- Don't add FK to ticks, lifecycle_events, or event_lifecycle_events. See above.
- Don't increase MaxOpenConns beyond 10. TickWriter serializes writes via single goroutine.
- `Close()` must be called after TickWriter exits, not before.
- `ApplyLifecycleEvent` only handles explicit WS events. Implicit transitions need REST scan.
- Multi-step writes must use transactions. See Transactions section above.
- To change default seed data (app_config, strategy_config, liquidity_pool), add a new migration `.sql` file — don't edit existing migrations.
- Raw SQL on `is_real` column must use `true`/`false`, not `1`/`0`. PG boolean — SQLite syntax doesn't work.
- `GetActiveMarkets` includes `determined` status. Scheduler must keep tracking through determination so the `settled` WS event arrives and `ApplyLifecycleEvent` resolves orders. Dropping subscription between `determined` and `settled` loses the settled event.
- `GetUnresolvedMarkets` third clause (result set + non-terminal real orders) catches markets where `determined` set the result but `settled` was missed. Without it, reconciler/backfill-orders skip markets that already have a result in DB — orders stay `filled` forever.
