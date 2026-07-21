# internal/algorithms

Pluggable trading strategies for Kalshi tennis markets. Strategies implement
the `Strategy` interface and can be dropped into the live WS processor or the
backtest engine — one source of truth for signal logic.

## Files

- `strategy.go` — `Strategy`, `OrderEmitter`, `PriceLookup`, `ScoreObserver` interfaces + adapters
- `matchpoint.go` — match point detection strategy (moved from `internal/signal/generator.go`)
- `setpoint.go` — set point detection strategy (configurable serve/return conversion probs)
- `fadelongshot.go` — fade longshot strategy (buy favorite at T-10min before close)
- `pointclass.go` — standalone point classification (is_break_point, is_set_point, is_match_point)
- `markov.go` — hierarchical Markov chain model for tennis win probability
- `breakpoint.go` — break point strategy using Markov fair value
- `convexpool.go` — convex pool strategy blending Markov + market price
- `multi.go` — `MultiStrategyRuntime` (fans out price + point events to multiple strategies)
- `quota.go` — `QuotaGuard` order throttle wrapper (cooldown, budget, rate limit, daily cap)
- `real_emitter.go` — `KalshiOrderEmitter` submits real IOC bid orders to Kalshi REST API
- `quota_test.go` — unit tests for QuotaGuard
- `set1winner.go` — set 1 winner strategy
- `comeback040.go` — comeback 0-40 strategy
- `setdown.go` — set down strategy
- `server1530.go` — server 15-30 strategy
- `tiebreak.go` — tiebreak strategy
- `tiebreak_server.go` — tiebreak server strategy
- `spike_fade.go` — spike fade strategy
- `calibrated_markov.go` — calibrated Markov strategy
- `surface_markov.go` — surface Markov strategy

## Interfaces

### Strategy

Core interface for all strategies. Methods:
- `OnPrice(marketTicker, price)` — called on every WS ticker or historical replay
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

### ScoreObserver

Optional interface for strategies that react to point-by-point score updates:
- `OnPoint(eventTicker, Point)` — called for each scored point during live ingestion or backtest replay

Implemented by: `MatchPointStrategy`, `SetPointStrategy`, `BreakPointStrategy`, `ConvexPoolStrategy`.
`MultiStrategyRuntime` fans out `OnPoint` to all wrapped strategies that implement `ScoreObserver`.

### PriceLookup

`GetPrice` / `GetPriceAge` for consumers like CloseTimer. Implemented by
`MatchPointStrategy`.

## MatchPointStrategy

Tracks market prices and emits buy orders when edge exceeds threshold.

- Buy only — never sell (comeback bets have 7.1% hit rate, catastrophic)
- Uses empirical conversion rate (97%) instead of hand-tuned formula
- Edge = (0.97 - market_price) * 100 cents; fires if edge >= 1 cent

## Architecture

- `MatchPointStrategy` holds thread-safe map of market_ticker -> latest YES price
- `OnPrice` called by WS manager on every ticker message
- Orders emitted via `OrderEmitter` — same logic in live and backtest

## Point Classification (`pointclass.go`)

Standalone function `ClassifyPoint(PointContext) PointClassification` computing three flags:
- `IsBreakPoint` — returner can win this point to break serve
- `IsSetPoint` — player can win this game to win the set
- `IsMatchPoint` — player can win this set to win the match

Used by:
- Live ingestion (`apitennis/scraper.go`) to set flags on `store.Point` before DB insert
- Strategies can also call it directly for re-derivation

**Gaps:**
- Does not handle tiebreak set points (returns false when `IsTiebreak=true`). Tiebreak points need separate classification logic — a tiebreak point at 6-6 in the set is both a set point and match point.
- `SetsHome`/`SetsAway` must be passed in correctly. Caller is responsible for tracking set count across the match. In live ingestion, these come from `HomeSetGames`/`AwaySetGames` which accumulate completed set scores. If the WebSocket feed sends partial data (only current set), set counts may be wrong.
- No `IsGamePoint` flag. Could be useful for strategies that care about any pressure point, not just break/set/match.

## MarkovModel (`markov.go`)

Hierarchical Markov chain computing tennis win probabilities from any score state.

Layers:
1. **Point win** — `P(point | server)` using `pServe` (default 0.64). Handles deuce/advantage recursion.
2. **Game win** — `P(game | server)` via point-level recursion. Handles 40-40, A-40, 40-A.
3. **Set win** — `P(set | server)` via game-level recursion. Handles 6-6 tiebreak via full tiebreak Markov chain (first to 7, win by 2, serve alternation 1-2-2-1...).
4. **Match win** — `P(match)` via set-level recursion. Best-of-3 only (first to 2 sets).

Key methods:
- `WinProbability(setsHome, setsAway, gamesHome, gamesAway, pointsHome, pointsAway, server, isTiebreak) → float64` — probability home wins match
- `FairValue(setsHome, setsAway, gamesHome, gamesAway, pointsHome, pointsAway, server, isTiebreak) → float64` — same, but from perspective of the player who is about to win the current point (used as fair YES price)

**Gaps:**
- **Best-of-3 only.** No support for best-of-5 (Grand Slams). Adding it requires changing `setsToWin` from 2 to 3 and is not parameterized.
- **Tiebreak model uses symmetric serve assumption.** At 0-0, tiebreak is exactly 50/50 because `pReturn = 1 - pServe` and serve alternates evenly. Non-50/50 only appears at mid-scores or with per-player serve strengths.
- **No serve strength parameterization per player.** Uses a single global `pServe`. In reality, each player has a different serve win rate. The model could accept per-player `pServe` values.
- **No caching/memoization.** Recomputes full recursion on every call. For live use this is fine (called once per point), but for backtest over thousands of points it could be slow. A memo table keyed by score state would help.
- **Deuce recursion depth.** The point-level recursion for deuce/advantage is mathematically exact (geometric series converges), but Go's floating point may lose precision after ~20+ deuce cycles. In practice this never happens (max ~5 deuce cycles in real tennis).

## BreakPointStrategy (`breakpoint.go`)

Buys the returner's market when a break point opportunity exists, using Markov fair value for edge calculation.

Logic:
1. `OnPoint` updates match state (sets won) and calls `processBreakPoint`
2. Checks if `IsBreakPoint` is true on the incoming point
3. Computes Markov `FairValue` for the returner's market
4. Edge = `(fairValue - marketPrice) * 100` cents
5. Fires buy if `edge >= MinEdgeCents` and `marketPrice <= MaxMarketPrice`
6. Size = `SuggestedSize` contracts

Config (`BreakPointConfig`):
- `PServe` — serve win probability (default 0.64)
- `MinEdgeCents` — minimum edge to fire (default 5)
- `MaxMarketPrice` — max price to buy at (default 0.50)
- `SuggestedSize` — order size in contracts (default 10)
- `Label` — strategy label for orders (default "breakpoint")

**Gaps:**
- **No sell logic.** Only buys. No mechanism to exit on serve hold (price drops back). The original design mentioned selling on serve hold, but this is not implemented.
- **No position tracking.** Doesn't know if it already has a position in this market. Could fire repeatedly on consecutive break points in the same game.
- **No per-market cooldown.** Unlike `MatchPointStrategy` which has built-in staleness, this relies on the `QuotaGuard` wrapper for throttling. Multiple break points in one game (15-40, 30-40, 40-40→A) could trigger multiple orders.
- **Tiebreak break points not handled.** `ClassifyPoint` returns `IsBreakPoint=false` for tiebreaks. In tiebreaks, every point on serve is effectively a break point (mini-break). Strategy misses these.
- **Markov fair value may not reflect real market.** The model uses a global `pServe` — if the actual server has a much higher/lower serve win rate, the fair value will be off.
- **No exit on match end.** Doesn't clean up state when match settles.

## ConvexPoolStrategy (`convexpool.go`)

Blends Markov fair value with market price using a convex combination (α blend). Fires on every point where the blended edge exceeds threshold.

Logic:
1. `OnPoint` updates match state and calls `processConvex`
2. For each market (home and away):
   - Computes Markov `FairValue` for that player
   - Blended = `α * fairValue + (1-α) * marketPrice`
   - Edge = `(blended - marketPrice) * 100` cents
   - Fires buy if `edge >= MinEdgeCents` and `marketPrice <= MaxMarketPrice`
3. Size = `SuggestedSize` contracts

Config (`ConvexPoolConfig`):
- `PServe` — serve win probability (default 0.64)
- `Alpha` — Markov weight in blend (default 0.50). `α=0` = pure market, `α=1` = pure Markov.
- `MinEdgeCents` — minimum blended edge (default 3)
- `MaxMarketPrice` — max price to buy at (default 0.95)
- `SuggestedSize` — order size (default 10)
- `Label` — strategy label (default "convexpool")

**Gaps:**
- **Same position tracking issue as BreakPointStrategy.** No dedup per market. Could fire on every point in a match.
- **Alpha is static.** A more sophisticated approach would vary α based on confidence (e.g., higher α when score is deep in the set, lower when early). Currently fixed.
- **Fires on both markets simultaneously.** If Markov says home is undervalued and away is undervalued (shouldn't happen mathematically, but rounding could cause it), both could fire. No cross-market consistency check.
- **No tiebreak handling.** Markov model's crude 50/50 tiebreak assumption means fair value in tiebreaks is unreliable. Strategy still fires in tiebreaks.
- **No exit logic.** Buy-only. No mechanism to sell when edge reverses.
- **No per-market cooldown.** Relies entirely on `QuotaGuard`.
- **MaxMarketPrice=0.85 is high.** Allows buying at prices where upside is only 15 cents. May not be profitable after fees.

## MultiStrategyRuntime (`multi.go`)

Wraps multiple strategies behind a single `Strategy` interface. Fans out:
- `OnPrice`, `RegisterMarkets`, `UnregisterMarkets`, `DeletePrice` to all wrapped strategies
- `OnPoint` to all wrapped strategies that implement `ScoreObserver`

Each strategy gets its own `TaggedEmitter` so orders are labeled with the strategy name.

**Gaps:**
- `OnPoint` fan-out is synchronous. A slow strategy blocks all subsequent strategies. In practice this is fine (strategies are fast), but could be made async with per-strategy goroutines.
- No error isolation. If one strategy panics in `OnPoint`, the whole runtime crashes. Could wrap each call in a recover.

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

Guards (checked in order):
0. **Pre-match gate** — looks up market via `GetMarket`, refuses if `occurrence_ts` is in future. Also populates `MatchTitle` and `PlayerName` on the order from market + event lookup.
1. **Strategy enabled** — checks `strategy_config` table.
2. **Trigger ranges** — price must fall within configured bands (if any).
3. **Kelly sizing** — computes size from bankroll + Kelly fraction. Sub-1 counts rounded up to 1 (Kalshi rejects fractional minimums).
4. **Liquidity pool** — deducts cost from pool, refunds on failure.

Safety:
- IOC by default (no resting orders)
- Kelly sizing with real bankroll (no $5 paper cap)
- Per-order context timeout (default 10s)
- `taker_at_cross` self-trade prevention
- All submissions logged with order_id, fill_count, remaining_count
- Errors logged, not propagated — strategy goroutines never block on REST
- `match_title` and `player_name` stored on every real order for dashboard display

## Gotchas

- Markets must be registered before signals can fire
- No price = no order. WS must be actively subscribed to the market
- Stale price (>60s) = no order. Protects against WS disconnects
- Paper trail always complete — `QuotaGuard.paper` receives every order regardless of throttle
- Real orders only when `real_trading_enabled: true` — otherwise inner is `NoopEmitter`
- `QuotaGuard` budget tracking is local (no REST balance query). `SuggestedSize` = spend per order.
- Two independent `QuotaGuard` instances in live mode: paper guard (paper budget) + real guard (real budget)
