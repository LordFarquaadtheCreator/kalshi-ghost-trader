# cmd/ghost-trader

Entrypoint. Wires all components via errgroup.

## Wiring order

1. Load app config (app.yaml / app.dev.yaml)
2. Open SQLite (db_path from app config)
3. Load runtime config from DB (merged with app config)
4. Load RSA signer
5. Create tick writer
6. Create REST client
7. Build order emission pipeline (paper guard + real guard + emitter)
8. Create strategies (all wired to paper guard as their OrderEmitter)
9. Create WS manager
10. Create tracker (wired to strategies)
11. Create scanner
12. Create scheduler
13. Launch goroutines via errgroup (metrics server, tick writer, WS, scanner, scheduler)

## Order emission pipeline

```
strategies → paperGuard → paperEmitter (TickWriter, ALWAYS)
                 ↓ (inner, if paper quota approved)
              realGuard → KalshiOrderEmitter (if real_trading_enabled)
                 ↓ (if real quota approved)
              NoopEmitter (if real trading disabled)
```

- Paper guard: always active, tracks `paper_budget_total` / `paper_budget_floor`
- Real guard: only when `real_trading_enabled: true`, tracks `real_budget_total` / `real_budget_floor`
- Both have independent cooldowns, rate limits, daily quotas
- Paper trail always complete — `paperEmitter` receives every order regardless
- When `order_quota_enabled: false`: paper guard passes all through (no throttle)
- When `real_trading_enabled: false`: inner is `NoopEmitter` (no real orders)
- Startup logs `REAL TRADING ENABLED` warning when real trading is on

## Shutdown

SIGINT/SIGTERM cancels root ctx. errgroup cancels all. Then:
- `tr.StopAll()` — unsubscribes all tracked markets
- `db.Close()` — closes SQLite (after tick writer flushed)

## Gotchas

- Don't move `db.Close()` before errgroup `Wait()`. Tick writer may still flush.
- Don't add goroutines outside errgroup. Won't get cancelled on signal.
- Metrics server bind address comes from `app.yaml` (`metrics_addr`). Empty string disables it.
- `guard.Close()` must be deferred — stops rate limiter goroutine.
- Real guard `Close()` must also be deferred when `real_trading_enabled`.
- Strategies receive `paperGuard` as their emitter — not the raw `TickWriterEmitter`.
- Never wire strategies directly to `KalshiOrderEmitter` — bypasses all safety layers.
