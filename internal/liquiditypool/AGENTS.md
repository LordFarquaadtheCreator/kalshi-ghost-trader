# internal/liquiditypool

Liquidity pool — single source of truth for real cash at risk. Kelly sizing reads `balance_cents` live per order.

## Functions

- `Get(ctx, db)` — read pool row (id=1 singleton)
- `Init(ctx, db, initialBalanceCents)` — seed if not exists (OnConflict DoNothing). Used by migration only.
- `Reset(ctx, db, initialBalanceCents)` — wipe balance, initial, spent, pnl. Set new starting point. Called by dashboard reset endpoint.
- `TopUp(ctx, db, addCents)` — add capital without wiping history. Increases balance + initial_balance. Called by dashboard topup endpoint.
- `Deduct(ctx, db, spendCents)` — atomic deduct on order submit. Fails if insufficient (prevents going negative). Returns new balance.
- `Refund(ctx, db, refundCents)` — atomic refund on cancel/fail. Returns new balance.

## Pool = Bankroll

`liquidity_pool.balance_cents` IS the kelly bankroll for real orders. `KalshiOrderEmitter.EmitOrder` fetches it live, sizes via `kellySizeRaw(convProb, marketPrice, balanceDollars, kellyFraction)`.

- Profit compounds: win → pool grows → next order sizes up
- Losses shrink: loss → pool shrinks → next order sizes down
- Hard cap on risk: pool at $20 → max loss $20 (orders skip when pool empty)
- Set via dashboard reset/topup, NOT `real_bankroll` config

## Dashboard endpoints

- `POST /api/liquidity-pool/reset` — body `{"balance_cents": 2000}`. Wipes history.
- `POST /api/liquidity-pool/topup` — body `{"add_cents": 500}`. Preserves history.
- `GET /api/liquidity-pool` — read pool state.

## Gotchas

- `Deduct` uses `WHERE balance_cents >= spendCents` — atomic, concurrency-safe. Concurrent orders can't drive pool negative.
- `Reset` wipes `total_spent_cents` and `total_pnl_cents`. Use for fresh start. Use `TopUp` to add capital mid-run.
- `TopUp` increases `initial_balance_cents` so P&L % stays meaningful against new contribution baseline.
- `real_bankroll` config is deprecated for sizing. Migration 0007 syncs pool to `real_bankroll` once on upgrade (only if pool at old $1000 seed); after that, dashboard owns it.
- Paper trading uses `paper_bankroll` config — separate, not pool-backed.
