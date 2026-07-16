# internal/signal

Match point signal generator + close timer strategy. Both emit simulated
buy orders to the `orders` table via TickWriter.

## Files

- `generator.go` — match point detection, price tracking, edge calc, order emission
- `close_timer.go` — close timer strategy (buy favorite N min before close)

## Strategies

### Match Point (generator.go)

Detects match points from FlashScore point data, looks up live Kalshi market
prices, emits simulated buy orders.

- Only fires when the match-point player is SERVING (97.3% conversion vs 88.5% returning)
- Buy only — never sell (comeback bets have 7.1% hit rate, catastrophic)
- Uses empirical conversion rate (97%) instead of hand-tuned probability formula
- Edge = (0.97 - market_price) * 100 cents; fires if edge ≥ 5 cents

### Close Timer (close_timer.go)

Polls DB for active markets approaching close_ts. At T-leadMin, picks the
higher-priced side (the favorite). If price ≥ minPrice, emits a buy order.

- Dedup per event — one order per event per close window
- No FlashScore needed — purely time + price based
- Backtest: favorite ≥85c at T-10min won 100% (Sharpe 1.01)
- Cleans up fired map when events leave the closing window

## Architecture

- `Generator` holds thread-safe map of market_ticker → latest YES price (updated by WS handleTicker)
- `CloseTimer` uses `Generator` as a `PriceLookup` (GetPrice interface)
- `RegisterMarkets` maps event_ticker → [home_market_ticker, away_market_ticker]
- `UpdatePrice` called by WS manager on every ticker message
- `OnPoints` called by FlashScore scraper after ingesting new points
- Orders emitted via `tickWriter.IngestOrder` — same single-writer architecture

## Gotchas

- Markets must be registered before match-point signals can fire (RegisterMarkets)
- Close timer needs WS prices — if WS is down, no orders fire (price cache stale)
- No price = no order. WS must be actively subscribed to the market
- Stale price (>60s) = no match-point order. Protects against WS disconnects
- Match point detection is conservative — requires both set and game position
- Set tracking relies on sequential point arrival — late/out-of-order points may miscount
- Orders are simulated only — no real trades executed

