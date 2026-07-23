# internal/strategies

Factory that wires all strategies into a single `algorithms.MultiStrategyRuntime`.
Called once from `main.go` for live mode. Backtest mode uses
`backtest.DefaultFactories()` instead (separate registry — see
`internal/backtest/AGENTS.md`).

## Files

- `builder.go` — `Build(emitter, db, log) *algorithms.MultiStrategyRuntime`

## Build

```go
multi := strategies.Build(paperQuotaGuard, db, log)
```

Constructs each strategy with its default config, registers under a name in
the factory map, returns the assembled `MultiStrategyRuntime`. The emitter
passed in is the paper-trading emitter (paper orders always flow); real
emitter is wired separately inside `KalshiOrderEmitter` via `QuotaGuard`.

## Strategies registered

| Name | Strategy | Notes |
|---|---|---|
| `matchpoint` | `MatchPointStrategy` | shared instance, also used by `close_timer` for price lookups |
| `matchpoint-aggro` | `SetPointStrategy` | set points excluded, returning only, aggro conversion probs |
| `setpoint` | `SetPointStrategy` | set points + returning |
| `setpoint-serve` | `SetPointStrategy` | set points, server only |
| `setpoint-cheap` | `SetPointStrategy` | set points + returning, max price 0.50 |
| `fadelongshot` | `FadeLongshotStrategy` | shared instance |
| `nofade` | `NoFadeStrategy` | shared instance |
| `breakback` | `BreakBackStrategy` | |
| `setdown` | `SetDownStrategy` | |
| `server1530` | `Server1530Strategy` | |
| `tiebreak` | `TiebreakStrategy` | |
| `breakpoint` | `BreakPointStrategy` | Markov fair value |
| `adout` | `AdOutStrategy` | |
| `convexpool` | `ConvexPoolStrategy` | Markov + market blend |
| `comeback040` | `Comeback040Strategy` | |
| `calibrated-markov` | `CalibratedMarkovStrategy` | needs DB |
| `cross-arb` | `CrossArbStrategy` | |
| `cross-arb-favorite` | `CrossArbFavoriteStrategy` | |
| `tiebreak-server` | `TiebreakServerStrategy` | |
| `set1winner` | `Set1WinnerStrategy` | |
| `volratio` | `VolumeRatioStrategy` | needs DB |
| `surface-markov` | `SurfaceMarkovStrategy` | needs DB |
| `spike-fade` | `SpikeFadeStrategy` | |
| `buythedip` | `BuyTheDipStrategy` | first with sell-to-close (TP/SL/time exit) |
| `fadelongshot-itf` | `FadeLongshotStrategy` | series-filtered: ITF |
| `fadelongshot-challenger` | `FadeLongshotStrategy` | series-filtered: Challenger |
| `fadelongshot-atp` | `FadeLongshotStrategy` | series-filtered: ATP |
| `fadelongshot-wta` | `FadeLongshotStrategy` | series-filtered: WTA |
| `fadelongshot-doubles` | `FadeLongshotStrategy` | series-filtered: all doubles |
| `fadelongshot-evening` | `FadeLongshotStrategy` | UTC hour window 18-4 |
| `setdown-series` | `SetDownStrategy` | DEEP_RESEARCH_2: positive-P&L series only |
| `setdown-noon` | `SetDownStrategy` | DEEP_RESEARCH_2: UTC 11-13 (Sharpe 1.17) |
| `tiebreak-itfwdoubles` | `TiebreakStrategy` | DEEP_RESEARCH_2: ITF women's doubles (Sharpe 2.07) |
| `tiebreak-eu-daytime` | `TiebreakStrategy` | DEEP_RESEARCH_2: UTC 10-16 |
| `cross-arb-favorite-itf` | `CrossArbFavoriteStrategy` | DEEP_RESEARCH_2: ITF men's only |
| `setpoint-set1` | `SetPointStrategy` | DEEP_RESEARCH_2: set 1 only |
| `convexpool-wta` | `ConvexPoolStrategy` | DEEP_RESEARCH_2: WTA only (Sharpe 0.39) |
| `doublebreak` | `DoubleBreakStrategy` | double break points (15-40, 30-40, 0-40) |
| `bookpressure` | `BookPressureStrategy` | bid/ask size imbalance, default 0.60 pressure |
| `bookpressure-strict` | `BookPressureStrategy` | 0.70 pressure, 500 min size, TP 3, SL 2 |
| `bookpressure-deep` | `BookPressureStrategy` | 0.75 pressure, 1000 min size, TP 4, SL 2, 180s |
| `bookpressure-elite` | `BookPressureStrategy` | 0.80 pressure, 2000 min size, TP 3, SL 2, 180s |
| `setwinner` | `SetWinnerStrategy` | Markov + per-set psychological adjustment |
| `setwinner-aggro` | `SetWinnerStrategy` | min edge 1, max price 0.95, cooldown 1 point |
| `setwinner-noadjust` | `SetWinnerStrategy` | ablation: pure Markov, no per-set adjustment |
| `close_timer` | `signal.CloseTimer` | uses `matchPoint` for `PriceLookup` |

## Gotchas

- `matchPoint` instance is shared between `matchpoint` strategy and `close_timer` (latter uses it as `PriceLookup`). Don't construct a second `MatchPointStrategy` for `close_timer`.
- `SetDB(db)` called on the multi runtime after construction — some strategies need DB access (`FadeLongshot`, `CalibratedMarkov`, `VolumeRatio`, `SurfaceMarkov`) and receive it via the runtime's DB setter.
- This is the LIVE registry. Backtest uses `backtest.DefaultFactories()` — keep both in sync when adding/removing strategies.
- All strategies participate in every match (no skipping). See "Simulated Trades" in root `AGENTS.md`.
- R.8: One shared `MarkovModel` (pServe=0.64) injected into `breakpoint`, `convexpool`, `convexpool-wta`, `setwinner`, `setwinner-aggro`, `setwinner-noadjust` via `SetSharedMarkovModel`. Memoization works across strategies — same score state computed once. `calibrated-markov` + `surface-markov` keep per-call models (different pServe parameterizations).
