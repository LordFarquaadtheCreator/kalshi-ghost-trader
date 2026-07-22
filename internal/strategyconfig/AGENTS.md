# internal/strategyconfig

CRUD for the `strategy_config` DB table. Per-strategy enable/disable flag for
real trading. Read by `KalshiOrderEmitter` before submitting a real order.

## Files

- `strategyconfig.go` — `GetAll`, `SetEnabled`

## API

- `GetAll(ctx, db) ([]store.StrategyConfigEntry, error)` — all rows
- `SetEnabled(ctx, db, strategy, enabled) error` — upsert (insert if missing, update `enabled` + `updated_ts` otherwise)

## Table

`strategy_config`:
- `strategy` (PK) — strategy label (matches `algorithms` strategy names)
- `enabled` — bool, gates real order emission
- `updated_ts` — millis

Seeded by migration. Dashboard toggles via `PUT /api/strategy-config`.

## Gotchas

- `SetEnabled` uses `ON CONFLICT (strategy) DO UPDATE` — upsert, idempotent.
- Read by `KalshiOrderEmitter` guard #1 (see `internal/algorithms/AGENTS.md`). If `enabled=false`, real order is skipped — paper order still flows.
- Default seed value is `false` for all strategies. Must explicitly enable in dashboard before real orders fire.
