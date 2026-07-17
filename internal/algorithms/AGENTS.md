# internal/algorithms

Pluggable trading strategies for Kalshi tennis markets. Strategies implement
the `Strategy` interface and can be dropped into the live WS processor or the
backtest engine — one source of truth for signal logic.

## Files

- `strategy.go` — `Strategy`, `OrderEmitter`, `PriceLookup` interfaces + adapters
- `matchpoint.go` — match point detection strategy (moved from `internal/signal/generator.go`)
- `setpoint.go` — set point detection strategy (configurable serve/return conversion probs)
- `fadelongshot.go` — fade longshot strategy (buy favorite at T-10min before close)
- `multi.go` — `MultiStrategyRuntime` (fans out events to multiple strategies)
- `quota.go` — `QuotaGuard` order throttle wrapper (cooldown, budget, rate limit, daily cap)
- `real_emitter.go` — `KalshiOrderEmitter` submits real IOC bid orders to Kalshi REST API
- `quota_test.go` — unit tests for QuotaGuard

## Interfaces

### Strategy

Core interface for all strategies. Methods:
- `OnPrice(marketTicker, price)` — called on every WS ticker or historical replay
- `OnPoints(pts)` — called when new point-by-point score data arrives
- `RegisterMarkets(eventTicker, marketTickers)` — associate event with its markets
- `UnregisterMarkets(eventTicker)` — cleanup all state for a match
- `DeletePrice(marketTicker)` — remove single market's price tracking

### OrderEmitter

Receives orders from strategies. Implementations:
- `TickWriterEmitter` — wraps `store.TickWriter` for live mode (paper trail)
- `OrderCollector` — in-memory collection for backtest
- `NoopEmitter` — discards (signal disabled or real trading off)
- `QuotaGuard` — wraps paper + inner emitter, applies 4-layer throttle
- `KalshiOrderEmitter` — submits real IOC bid orders to Kalshi REST API

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

## QuotaGuard

Wraps two emitters: `paper` (always receives all orders) and `inner` (receives
only quota-approved orders). Implements `OrderEmitter`.

4 throttle layers (checked in order):
1. **Per-market cooldown** — first order per market passes, rest dropped within window
2. **Budget floor** — `atomic.Int64` spend tracking in cents. Rollback on drop. No REST balance query.
3. **Global rate limit** — token bucket, non-blocking. Drops if no token.
4. **Daily quota** — `atomic.Int64` counter, hard ceiling.

When `Enabled=false`, all orders pass to paper only — inner is `NoopEmitter`.

`RemainingBudget()` returns remaining budget in dollars (-1 = no tracking).
`ResetDailyQuota()` resets the daily counter.
`Close()` stops the rate limiter goroutine.

## KalshiOrderEmitter

Submits real IOC bid orders to `POST /portfolio/events/orders` (V2 endpoint).

Safety:
- IOC by default (no resting orders)
- Hard contract cap (`MaxContracts`, default 50) — clamps oversized orders
- Per-order context timeout (default 10s)
- `taker_at_cross` self-trade prevention
- All submissions logged with order_id, fill_count, remaining_count
- Errors logged, not propagated — strategy goroutines never block on REST

## Gotchas

- Markets must be registered before match-point signals can fire
- No price = no order. WS must be actively subscribed to the market
- Stale price (>60s) = no order. Protects against WS disconnects
- Match point detection is conservative — requires both set and game position
- Set tracking relies on sequential point arrival — late/out-of-order points may miscount
- Paper trail always complete — `QuotaGuard.paper` receives every order regardless of throttle
- Real orders only when `real_trading_enabled: true` — otherwise inner is `NoopEmitter`
- `QuotaGuard` budget tracking is local (no REST balance query). `SuggestedSize` = spend per order.
- Two independent `QuotaGuard` instances in live mode: paper guard (paper budget) + real guard (real budget)
