# internal/store

SQLite layer. Single-writer architecture via TickWriter.

## Files

- `store.go` — DB struct, New, Close, migrate
- `schema.go` — schemaDDL constant (full DDL)
- `types.go` — Event, Market, Tick, LifecycleEvent, EventLifecycleEvent, OrderbookEvent
- `events.go` — UpsertEvent, UpsertEventCheckNew
- `markets.go` — UpsertMarket, UpsertMarketCheckNew, GetActiveMarkets, GetMarketsByEvent, scanMarket helper
- `ticks.go` — InsertTickBatch
- `orderbook.go` — InsertOrderbookBatch
- `lifecycle.go` — InsertLifecycleEvent, InsertEventLifecycleEvent, ApplyLifecycleEvent
- `scan.go` — RecordScanRun
- `tickwriter.go` — TickWriter goroutine (batched writes)

## PRAGMA

WAL mode, synchronous=NORMAL, busy_timeout=5000, cache_size=-64000, temp_store=MEMORY, foreign_keys=ON.

Set in DSN so every pooled connection gets them.

## Connection pool

MaxOpenConns=1, MaxIdleConns=1. Single writer. SQLite serializes writes anyway.

## Tables

- `events` — tennis match events. PK: event_ticker.
- `markets` — 2 per event (one per player). PK: market_ticker. FK: event_ticker.
- `ticks` — every WS message (ticker, trade). No FK to markets. Log table — never reject. Extracted hot fields + raw JSON payload.
- `orderbook_events` — orderbook snapshots + deltas. No FK. Same reason. Delta: price/delta/side extracted. Snapshot: full levels in payload.
- `lifecycle_events` — market_lifecycle_v2 WS events. No FK. Same reason.
- `event_lifecycle_events` — event_lifecycle WS messages (event creation). No FK.
- `scan_runs` — scan audit log.

## Why no FK on ticks/orderbook_events/lifecycle_events/event_lifecycle_events

WS messages can arrive before scanner stores the market. FK would reject the tick. Data loss. Log tables must never reject.

## TickWriter

Single goroutine. Batches inserts. Four channels: `in` (ticks, 8192 buffer), `orderbookIn` (orderbook events, 8192 buffer), `lifecycleIn` (lifecycle, 1024 buffer), `eventLifecycleIn` (event lifecycle, 1024 buffer). Non-blocking ingest — drops on full buffer with warning.

Flush triggers: batch full, timer fires, lifecycle event arrives, ctx cancelled.

After inserting a lifecycle event, calls `ApplyLifecycleEvent` to update `markets` table status. Maps: activated→active (also updates open_ts if present), deactivated→inactive, determined→determined (updates result+settlement_ts), settled→finalized (updates result+settlement_ts), close_date_updated→close_ts only. Each type only updates its own columns — preserves close_ts/settlement_ts from other sources. Implicit transitions (initialized→active, active→closed) emit no WS event — rely on REST scan.

## Upsert pattern

`INSERT OR IGNORE` + check `RowsAffected()`. If 0, row existed — run UPDATE. `ON CONFLICT DO UPDATE` returns 1 for both insert and update, can't distinguish new vs existing.

## Gotchas

- Don't add FK to ticks, lifecycle_events, or event_lifecycle_events. See above.
- Don't increase MaxOpenConns. SQLite + WAL still serializes writes. Multiple writers = lock contention.
- `Close()` must be called after TickWriter exits, not before.
- `ApplyLifecycleEvent` only handles explicit WS events. Implicit transitions need REST scan.
