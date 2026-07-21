# Trading Strategies

## Overview

Pluggable strategies implement the `Strategy` interface. Same logic runs in
live WS processing and backtest replay — one source of truth.

Simulated trades **always** run all strategies. No skipping, filtering, or
conditional activation. Every strategy participates in every match.

## Interfaces

### Strategy

```go
type Strategy interface {
    OnPrice(marketTicker string, price float64)
    RegisterMarkets(eventTicker string, marketTickers []string)
    UnregisterMarkets(eventTicker string)
    DeletePrice(marketTicker string)
    OnTick(ctx context.Context)
}
```

### ScoreObserver

Optional interface for strategies reacting to point-by-point updates:

```go
type ScoreObserver interface {
    OnPoint(eventTicker string, p store.Point)
}
```

Implemented by: `MatchPointStrategy`, `SetPointStrategy`, `BreakPointStrategy`,
`ConvexPoolStrategy`, `CalibratedMarkov`, `SurfaceMarkov`, and others.
`MultiStrategyRuntime` fans out `OnPoint` to all wrapped strategies that implement it.

### PreMatchGated

Strategies that should not receive `OnPrice` until match starts (first `OnPoint`
received). Prevents premature orders from pre-match price movements.

Implemented by: breakback, setdown, server1530, tiebreak, tiebreakserver.
NOT implemented by: fadelongshot, nofade (their own `close_ts` gating suffices).

### PriceLookup

`GetPrice` / `GetPriceAge` for consumers like CloseTimer. Implemented by
`MatchPointStrategy`.

## Order Emission

### OrderEmitter interface

Implementations:
- `TickWriterEmitter` — wraps `store.TickWriter` for live mode (paper trail)
- `OrderCollector` — in-memory collection for backtest
- `NoopEmitter` — discards (signal disabled or real trading off)
- `QuotaGuard` — wraps paper + inner emitter, applies 4-layer throttle
- `KalshiOrderEmitter` — submits real IOC bid orders to Kalshi REST API
- `LogEmitter` — wraps emitter, logs each order
- `EnrichEmitter` — wraps emitter, populates PlayerName + MatchTitle via DB lookup

### QuotaGuard

4 throttle layers (checked in order):
1. **Per-market cooldown** — first order per market passes, rest dropped within window
2. **Budget floor** — `atomic.Int64` spend tracking in cents. Rollback on drop.
3. **Global rate limit** — token bucket, non-blocking. Drops if no token.
4. **Daily quota** — `atomic.Int64` counter, hard ceiling.

When `Enabled=false`, all orders pass to paper only — inner is `NoopEmitter`.

### KalshiOrderEmitter

Submits real IOC bid orders to `POST /portfolio/events/orders` (V2 endpoint).
Guards: min size, max price, duplicate detection, balance check.

## Order Sizing — Kelly Criterion

Fractional Kelly criterion with $5 cost cap (paper trading safety).

```
fKelly = (convProb - marketPrice) / (1 - marketPrice)
size = kellyFraction * fKelly * bankroll
maxSize = 5.0 / marketPrice  // cost cap
```

Package-level params (set from config):
- `paperBankroll` — default 1000
- `realBankroll` — default 1000
- `kellyFractionP` — default 0.25

## Markov Model (`markov.go`)

Hierarchical Markov chain computing tennis win probabilities from any score state.

### Layers

1. **Point win** — `P(point | server)` using `pServe` (default 0.64). Handles deuce/advantage recursion.
2. **Game win** — `P(game | server)` via point-level recursion. Handles 40-40, A-40, 40-A.
3. **Set win** — `P(set | server)` via game-level recursion. At 6-6: full tiebreak Markov chain (first to 7, win by 2, serve alternation 1-2-2-1...). Mid-tiebreak: uses current TB score + correct serve alternation.
4. **Match win** — `P(match)` via set-level recursion. Best-of-3 only (first to 2 sets).

### Key methods

- `WinProbability(setsHome, setsAway, gamesHome, gamesAway, pointsHome, pointsAway, server, isTiebreak) → float64`
- `FairValue(...) → float64` — same, from perspective of player about to win current point (fair YES price). Clamped to [0.01, 0.99].

### Tiebreak model

Full recursive tiebreak: first to 7, win by 2. Serve alternates 1-2-2-1-1-2-2-1...
`tbPointValue` parses numeric tiebreak scores ("0", "1", "2", ...) — separate
from `pointValueMarkov` which handles regular game points ("0", "15", "30", "40", "A").

With symmetric `pReturn = 1 - pServe`, 0-0 tiebreak is exactly 50/50 (serve
alternates evenly). Non-50/50 appears at mid-scores or with per-player serve strengths.

Deuce handling at 7-7+: closed-form geometric series (same pattern as game deuce).

### Known gaps

- **Best-of-3 only.** `setsToWin` is constant 2. No best-of-5 (Grand Slams).
- **Symmetric serve assumption.** Single global `pServe`. No per-player serve strengths.
- **No caching/memoization.** Recomputes full recursion on every call. Fine for live (once per point), slow for backtest over thousands of points.

## Strategy Catalog

| Strategy | File | Trigger | ScoreObserver | PreMatchGated | Description |
|---|---|---|---|---|---|
| MatchPoint | `matchpoint.go` | Price + point | Yes | No | Match point detection. Buy when edge ≥ 1 cent. 97% empirical conversion rate. Buy-only. |
| SetPoint | `setpoint.go` | Price + point | Yes | No | Set point detection. Configurable serve/return conversion probs. |
| FadeLongshot | `fadelongshot.go` | Price + time | No | No | Buy favorite at T-10min before close. Fade longshot pricing. |
| NoFade | `nofade.go` | Price + time | No | No | Variant of fadelongshot without fading. |
| BreakPoint | `breakpoint.go` | Point | Yes | No | Buy returner's market at break point. Markov fair value for edge. |
| ConvexPool | `convexpool.go` | Point | Yes | No | Blend Markov fair value with market price (α blend). Fire on blended edge. |
| CalibratedMarkov | `calibrated_markov.go` | Point | Yes | No | Markov with calibration adjustments. |
| SurfaceMarkov | `surface_markov.go` | Point | Yes | No | Surface-specific Markov (adjusts pServe by surface). |
| BreakBack | `breakback.go` | Point | Yes | Yes | Back break detection — buy after break occurs. |
| Comeback040 | `comeback040.go` | Point | Yes | Yes | Comeback from 0-40 deficit. |
| SetDown | `setdown.go` | Point | Yes | Yes | Buy after losing first set (value opportunity). |
| Server1530 | `server1530.go` | Point | Yes | Yes | Server at 15-30 or 30-40 pressure. |
| Tiebreak | `tiebreak.go` | Point | Yes | Yes | Tiebreak-specific strategy. |
| TiebreakServer | `tiebreak_server.go` | Point | Yes | Yes | Tiebreak server advantage. |
| Set1Winner | `set1winner.go` | Point | Yes | No | First set winner detection. |
| SpikeFade | `spike_fade.go` | Price | No | No | Fade price spikes. |
| CrossArb | `crossarb.go` | Price | No | No | Cross-market arbitrage detection. |
| VolRatio | `volratio.go` | Price | No | No | Volume ratio analysis. |

## MultiStrategyRuntime (`multi.go`)

Wraps multiple strategies behind a single `Strategy` interface. Fans out
`OnPrice`, `RegisterMarkets`, `UnregisterMarkets`, `DeletePrice` to all.
Fans out `OnPoint` to all that implement `ScoreObserver`.

Each strategy gets its own `TaggedEmitter` so orders are labeled with strategy name.

## Point Classification (`pointclass.go`)

Standalone `ClassifyPoint(PointContext) PointClassification`:
- `IsBreakPoint` — returner can win this point to break serve
- `IsSetPoint` — player can win this game to win the set
- `IsMatchPoint` — player can win this set to win the match

Used by live ingestion (`apitennis/scraper.go`) to set flags on `store.Point`
before DB insert. Strategies can also call directly.

**Gap:** Does not handle tiebreak set points (returns false when `IsTiebreak=true`).

## Backtest

Replay historical tick data through strategies and report P&L.

```bash
go run ./cmd/backtest -strategy matchpoint
go run ./cmd/backtest -strategy matchpoint -debug   # log filter reasons
```

Strategies register in `strategies` map in `cmd/backtest/main.go`.
Must implement `replayStrategy` (Strategy + `SetReplayTime` + `OnPriceAt`).

Backtest engine interleaves price ticks and point events by timestamp.
Result cache: 5min TTL.

## Price Band Analysis

Cron goroutine in `internal/pricebands/` runs daily, computes days not
yet in `price_band_results` table. Dashboard `/price-bands` page displays
results with charts + filters.

Buckets orders into 12 fixed price bands. Per-day per-strategy per-band
aggregates: N, wins, win rate, net P&L, ROI, avg edge.
