# cmd/ghost-trader

Entrypoint. Wires all components via errgroup.

## Wiring order

1. Load app config (`appconfig.Load` — app.yaml / app.dev.yaml)
2. Open SQLite (`store.New`, WAL mode)
3. Run schema migrations (`db.Migrate`)
4. Load runtime config from DB (`config.Load` — merged with app config)
5. Load RSA signer (`kalshiAuth.NewSignerFromFile`)
6. Set sizing params (`algorithms.SetSizingParams`, `algorithms.SetRealBankroll`)
7. Create tick writer (`db.NewTickWriter`)
8. Create two REST clients:
   - `restClient` — shared by scanner, tracker, livedata, reconciler, orderbackfill, schedulechecker
   - `orderClient` — dedicated to order submission (isolated rate limit)
9. Build order emission pipeline (see below)
10. Build strategies (`strategies.Build` — all wired to paper guard as their OrderEmitter)
11. Create WS manager (`wsclient.NewManager`), set `multi` as price updater
12. Create API-Tennis scraper (`apitennis.New` — mandatory, primary score source)
13. Create Kalshi live-data poller (optional — `kalshilivedata.New`, if `kalshi_livedata_enabled`)
14. Compose score poller (`MultiScorePoller` if both, else single)
15. Create tracker (`tracker.New`), set `multi` as price cleaner + market registrar
16. Create scanner (`scanner.New`)
17. Create scheduler (`scheduler.New`)
18. Create backtest engine + cache (`backtest.NewEngine`, `backtest.NewCache`)
19. Create dashboard API server (`dashboardapi.NewServer` — metrics + pprof + strategy API)
20. Launch goroutines via errgroup (see below)

## Order emission pipeline

```
strategies → paperQuotaGuard → paperEmitter (EnrichEmitter → TickWriterEmitter, ALWAYS)
                 ↓ (inner, if paper quota approved)
              LiveToggleEmitter
                 ↓ (checks real_trading_enabled per EmitOrder)
              realQuotaGuard → KalshiOrderEmitter (orderClient)
                 ↓ (if real quota approved)
              NoopEmitter (if real trading disabled)
```

- `paperEmitter` = `EnrichEmitter` wrapping `TickWriterEmitter` — enriches orders with event/market metadata before persist
- Paper guard: always active, tracks `paper_budget_total` / `paper_budget_floor`
- Real guard: always constructed. Inner = `KalshiOrderEmitter` when approved, `NoopEmitter` when denied
- `LiveToggleEmitter` wraps `realQuotaGuard` — checks `real_trading_enabled` per EmitOrder, so dashboard flip takes effect on next order without restart
- Both guards have independent cooldowns, rate limits, daily quotas
- Paper trail always complete — `paperEmitter` receives every order regardless
- When `order_quota_enabled: false`: paper guard passes all through (no throttle)
- Startup logs `REAL TRADING ENABLED` warning when real trading is on
- `guard.Close()` deferred for both guards — stops rate limiter goroutines

## Goroutines launched via errgroup

1. Metrics/API server (`dashboardapi` — strategy API + pprof + runtime metrics)
2. Tick writer (single SQLite writer)
3. WS manager (auto-reconnect, dispatch)
4. Scanner loop (daily REST scan, `scan_interval_hours`)
5. Scheduler loop (poll DB, schedule tracking at `occurrence_datetime - lead`)
6. Reconciler loop (fill settlement gaps via REST for unresolved markets)
7. Order backfill loop (refresh stale real order status from REST)
8. Schedule checker loop (refresh stale `occurrence_ts` from REST)
9. API-Tennis scraper (always — WS real-time push, primary score source)
10. Kalshi live-data poller (optional — blocks until ctx cancelled for clean shutdown; per-match goroutines launched via tracker)
11. Strategy timer (`multi.RunTimer` — drives periodic `OnTick` calls for close_timer etc)
12. Backtest cache prewarm (runs all strategies every TTL, keeps cache fresh)

## Shutdown

SIGINT/SIGTERM cancels root ctx. errgroup cancels all. Then:
- `tr.StopAll()` — unsubscribes all tracked markets
- `db.Close()` — closes SQLite (after tick writer flushed, via `defer`)
- `btEngine.Close()` — closes backtest engine (via `defer`)

## Gotchas

- Don't move `db.Close()` before errgroup `Wait()`. Tick writer may still flush.
- Don't add goroutines outside errgroup. Won't get cancelled on signal.
- Metrics/API server bind address comes from `app.yaml` (`metrics_addr`). Empty string disables it.
- Two REST clients — don't reuse `restClient` for order submission. `orderClient` has isolated rate limit.
- `EnrichEmitter` must wrap `TickWriterEmitter` — orders persisted with event/market metadata.
- `LiveToggleEmitter` checks `real_trading_enabled` per EmitOrder — dashboard flip takes effect on next order, no restart needed.
- API-Tennis scraper is mandatory, not optional. Always wired as score poller.
- `MultiScorePoller` wraps both score sources when Kalshi live-data enabled.
- `tracker.SetPriceCleaner(multi)` + `tracker.SetMarketRegistrar(multi)` — strategies handle stale price cleanup + market registration.
- `wsMgr.SetPriceUpdater(multi)` — strategies receive WS price updates.
- Backtest cache prewarm runs immediately at startup, then every TTL.
- `guard.Close()` must be deferred for both guards — stops rate limiter goroutines.
- Strategies receive `paperQuotaGuard` as their emitter — not the raw `TickWriterEmitter`.
- Never wire strategies directly to `KalshiOrderEmitter` — bypasses all safety layers.
