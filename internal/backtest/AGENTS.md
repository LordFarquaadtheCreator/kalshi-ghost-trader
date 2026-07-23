# internal/backtest

Replay-only engine for backtesting trading strategies against historical
tick + point data from PostgreSQL. Dashboard live queries live in
`internal/dashboarddata` — do not add them here.

## Files

- `engine.go` — `Engine` type, `NewEngine(log, dsn)`, `RunStrategy`, `RunAll`, public types (`Order`, `Summary`, `StrategyResult`, `MarketRow`, `TickPrice`, `TickVolume`, interfaces)
- `loader.go` — `load()` fetches finalized markets, tick prices, tick volumes, points into memory. Called once at construction.
- `replay.go` — `replayInterleaved` (merges price ticks + score events by ts), `runCloseTimeBacktest` (close_ts path), `wireStrategyContext` (series/surface/volume setters), `orderMarketsByTitle`
- `resolve.go` — `resolveOrdersWithSells` (FIFO sell-aware PnL), `computeSummary` (risk-adjusted metrics)
- `factories.go` — `DefaultFactories()` strategy registry (single source of truth for CLI + dashboard + live)
- `persist.go` — `RecomputeIfNeeded` (cron-triggered recompute when new finalized markets appear), `CumPnLPoint` (cumulative P&L series type), `cumulativePnLSeries` (ordered [ts, cum_pnl] pairs for charting)

## Architecture

`Engine` holds loaded DB data in memory. `NewEngine` opens gorm, calls `load()`,
returns. `RunStrategy(name, minPrice)` replays all finalized matches through
the strategy, returns `StrategyResult` with orders + summary. `RunAll` fans
out across strategies in parallel goroutines.

Two replay paths per strategy:
1. **Tick replay** — interleaves price ticks + score events by timestamp. For
   strategies implementing `ScoreObserver` (matchpoint, setpoint, breakpoint,
   convexpool, etc).
2. **Close-time replay** — for strategies implementing `CloseTimeStrategy`
   (fadelongshot family). Replays all ticks per market, registers close_ts
   first. Only runs if strategy implements the interface.

## PnL Resolution

`resolveOrdersWithSells` handles buy + sell orders with FIFO matching:
- Sells match to prior buys on same market (FIFO queue).
- Matched buy PnL zeroed; sell carries round-trip PnL.
- Unmatched buys settle at market close (result yes/no).
- Unmatched sells skipped (naked short — position pipeline rejects these live).

For buy-only strategies (all current strategies), equivalent to per-order
settlement. Sell path exists for future sell-aware strategies and unifies
CLI + dashboard PnL (previously CLI had FIFO, engine had buy-only — bug).

## Strategy Factories

`DefaultFactories()` is the single registry. CLI, dashboard, and live mode
all reference it. Adding a strategy = adding one entry here + implementing
`algorithms.Strategy` + `ReplayStrategy` (SetReplayTime + OnPriceAt).

Optional interfaces strategies can implement:
- `CloseTimeStrategy` — needs close_ts (fadelongshot)
- `SeriesSetter` — needs series_ticker (fadelongshot variants)
- `SurfaceSetter` — needs court surface (surface-markov)
- `VolumeSetter` — needs dollar_volume series (volratio)
- `BookSetter` — needs bid/ask/sizes series (bookpressure)
- `algorithms.ScoreObserver` — wants point-by-point score updates

## Gotchas

- `NewEngine(log, dsn)` — DSN passed explicitly. Do NOT read `config.Cfg.DBDSN`
  here; CLI doesn't load `config.Cfg` (only `appconfig.Load`).
- `load()` only fetches `status = 'finalized'` markets. Unfinalized markets
  have no result — can't compute PnL.
- `RunAll` runs strategies in parallel. Engine data is read-only during
  replay (each strategy gets its own collector), so no locking needed.
- `resolveOrdersWithSells` mutates `orders[i].PnL` / `Won` / `Context` in
  place when matching sells — needed for FIFO zeroing of matched buys.
- Dashboard live queries (LatestScores, GetPaperOrdersPage, GetPassedMatches,
  etc) belong in `internal/dashboarddata`, NOT here.
