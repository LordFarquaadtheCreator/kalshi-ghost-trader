# internal/algorithms

Pluggable trading strategies for Kalshi tennis markets. Strategies implement
the `Strategy` interface and can be dropped into the live WS processor or the
backtest engine — one source of truth for signal logic.

## Files

- `strategy.go` — `Strategy`, `OrderEmitter`, `PriceLookup` interfaces + adapters
- `matchpoint.go` — match point detection strategy (moved from `internal/signal/generator.go`)

## Interfaces

### Strategy

Core interface for all strategies. Methods:
- `OnPrice(marketTicker, price)` — called on every WS ticker or historical replay
- `OnPoints(pts)` — called when new point-by-point score data arrives
- `RegisterMarkets(eventTicker, marketTickers)` — associate event with its markets
- `UnregisterMarkets(eventTicker)` — cleanup all state for a match
- `DeletePrice(marketTicker)` — remove single market's price tracking

### OrderEmitter

Receives simulated orders. Implementations:
- `TickWriterEmitter` — wraps `store.TickWriter` for live mode
- `OrderCollector` — in-memory collection for backtest
- `NoopEmitter` — discards (signal disabled)

### PriceLookup

`GetPrice` / `GetPriceAge` for consumers like CloseTimer. Implemented by
`MatchPointStrategy`.

## MatchPointStrategy

Detects match points from point data, looks up live market prices,
emits simulated buy orders.

- Only fires when the match-point player is SERVING (97.3% conversion vs 88.5% returning)
- Buy only — never sell (comeback bets have 7.1% hit rate, catastrophic)
- Uses empirical conversion rate (97%) instead of hand-tuned formula
- Edge = (0.97 - market_price) * 100 cents; fires if edge >= 1 cent

## Architecture

- `MatchPointStrategy` holds thread-safe map of market_ticker -> latest YES price
- `OnPrice` called by WS manager on every ticker message
- `OnPoints` called by FlashScore/API-Tennis scraper after ingesting new points
- Orders emitted via `OrderEmitter` — same logic in live and backtest

## Gotchas

- Markets must be registered before match-point signals can fire
- No price = no order. WS must be actively subscribed to the market
- Stale price (>60s) = no order. Protects against WS disconnects
- Match point detection is conservative — requires both set and game position
- Set tracking relies on sequential point arrival — late/out-of-order points may miscount
- Orders are simulated only — no real trades executed
