# internal/signal

Close timer strategy. Emits simulated buy orders to the `orders` table
via TickWriter.

## Files

- `close_timer.go` — close timer strategy (buy favorite N min before close)

## Strategies

### Close Timer (close_timer.go)

Polls DB for active markets approaching close_ts. At T-leadMin, picks the
higher-priced side (the favorite). If price ≥ minPrice, emits a buy order.

- Dedup per event — one order per event per close window
- No FlashScore needed — purely time + price based
- Backtest: favorite ≥85c at T-10min won 100% (Sharpe 1.01)
- Cleans up fired map when events leave the closing window
- Uses `algorithms.PriceLookup` interface for price queries (implemented by `MatchPointStrategy`)

## Architecture

- `CloseTimer` uses `algorithms.PriceLookup` (GetPrice/GetPriceAge) to query live prices
- Match point detection now lives in `internal/algorithms/matchpoint.go`
- Orders emitted via `tickWriter.IngestOrder` — same single-writer architecture

## Gotchas

- Close timer needs WS prices — if WS is down, no orders fire (price cache stale)
- Stale price (>60s) = no order. Protects against WS disconnects
- Orders are simulated only — no real trades executed
