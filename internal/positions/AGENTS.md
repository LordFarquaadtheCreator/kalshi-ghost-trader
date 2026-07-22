# internal/positions

Position lifecycle for the sell-to-close pipeline. One `Position` row per
`(market_ticker, strategy, is_real)`. Wired into `store` via
`SetPositionSettler` at startup — when a market settles via WS, open positions
are settled too.

## Files

- `manager.go` — `Manager` type, `New`, `ApplyBuy`, `ApplySell`, `Settle`, `GetOpen`, `GetOpenForStrategy`
- `clock.go` — logical clock for ordering events within a match
- `manager_test.go` — tests

## Model

```
Buy  (side="open")  → FilledBuyCount += n; reweight AvgEntryPrice
Sell (side="close") → FilledSellCount += n; reweight AvgExitPrice; realize PnL
                      = (avg_exit - avg_entry) * fill_count * 100
When FilledSellCount == FilledBuyCount → status="closed"
```

Sell-to-close only. Rejects sells with no open long position (`ErrNoOpenPosition`)
or sells exceeding open contract count (`ErrInsufficientSize`). No naked shorts.

At market settlement, `Settle` marks remaining open contracts
(`FilledBuyCount - FilledSellCount`) at $1 if won or $0 if lost, computing
settlement PnL.

## Backward compat

Legacy orders (`side=NULL`, no `position_id`) bypass this package. Reconciler's
legacy `ResolveRealOrders` / `ResolveSimulatedOrders` still handles them. New
orders with `side` set flow through `ApplyBuy` / `ApplySell`.

## API

- `New(db *gorm.DB) *Manager`
- `ApplyBuy(ctx, order) error` — open / add to long position
- `ApplySell(ctx, order) error` — close long, realize PnL, reject if no open
- `Settle(ctx, marketTicker, result) error` — settle remaining open contracts at market result
- `GetOpen(ctx, marketTicker) ([]Position, error)` — all open positions for a market
- `GetOpenForStrategy(ctx, marketTicker, strategy, isReal) (*Position, error)` — single position lookup

## Gotchas

- All methods transactional — read position, mutate, write back atomically.
- `ApplySell` rejects if `FilledSellCount + n > FilledBuyCount`. Partial sells OK if under open count.
- `Settle` is idempotent — skips positions already in `closed` or `settled` status.
- `Manager` implements `store.PositionSettler` — wired via `store.SetPositionSettler(positions.New(db.GormDB()))` in `main.go`.
- Don't call `ApplyBuy`/`ApplySell` for legacy orders (no `side`). They have no `position_id` and would create orphan positions.
