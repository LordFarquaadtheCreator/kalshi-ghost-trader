# cmd/ghost-trader

Entrypoint. Wires all components via errgroup.

## Wiring order

1. Load config
2. Load RSA signer
3. Open SQLite
4. Create tick writer
5. Create REST client
6. Build order emission pipeline (paper guard + real guard + emitter)
7. Create strategies (all wired to paper guard as their OrderEmitter)
8. Create WS manager
9. Create tracker (wired to strategies)
10. Create scanner
11. Create scheduler
12. Launch goroutines via errgroup (metrics server, tick writer, WS, scanner, scheduler)

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
- Metrics server binds 127.0.0.1 only. Not exposed externally.
- `METRICS_PORT=0` disables metrics server.
- `guard.Close()` must be deferred — stops rate limiter goroutine.
- Real guard `Close()` must also be deferred when `real_trading_enabled`.
- Strategies receive `paperGuard` as their emitter — not the raw `TickWriterEmitter`.
- Never wire strategies directly to `KalshiOrderEmitter` — bypasses all safety layers.
