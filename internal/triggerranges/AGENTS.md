# internal/triggerranges

CRUD for the `trigger_ranges` DB table. Per-strategy price bands gating real
order emission. Read by `KalshiOrderEmitter` guard #2 before submitting a real
order.

## Files

- `triggerranges.go` — `Get`, `Replace`, `IsPriceIn`, `Has`

## API

- `Get(ctx, db, strategy) ([]store.TriggerRange, error)` — all ranges for a strategy, ordered by `created_ts`
- `Replace(ctx, db, strategy, ranges) error` — transactional delete-all + insert. Stamps `created_ts` on insert.
- `IsPriceIn(ctx, db, strategy, price) (bool, error)` — true if price falls in any enabled range
- `Has(ctx, db, strategy) (bool, error)` — true if any ranges configured (enabled or not)

## Table

`trigger_ranges`:
- `id` (PK)
- `strategy` — strategy label
- `min_price`, `max_price` — inclusive band bounds (0.0-1.0)
- `enabled` — bool
- `source` — optional origin tag (e.g. "dashboard", "migration")
- `created_ts` — millis

Seeded by migration `0005_convexpool_trigger_ranges.sql`. Dashboard edits via
`PUT /api/trigger-ranges`.

## Gotchas

- `Replace` is delete-then-insert in a transaction. Loses individual range history — only the latest set is retained.
- `IsPriceIn` checks `enabled = true` ranges only. Disabled ranges are inert.
- `KalshiOrderEmitter` calls `Has` first; if no ranges configured for a strategy, the gate is skipped (no restriction). If ranges exist, `IsPriceIn` must return true.
- `Replace` overwrites `strategy` + `created_ts` on every range — caller doesn't need to set them.
