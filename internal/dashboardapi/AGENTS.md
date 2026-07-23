# internal/dashboardapi

HTTP server for dashboard + pprof + runtime metrics. Single `Server` type
mounted on `config.Cfg.MetricsAddr` (default `127.0.0.1:6060`).

## Files

- `server.go` — `Server`, `Deps`, `NewServer`, `Handler()`, all handlers, CORS wrapper

## Deps

```go
type Deps struct {
    Tracker   *tracker.Tracker
    Engine    *backtest.Engine
    LiveStore *dashboarddata.LiveStore
    DB        *store.DB
    Log       *slog.Logger
}
```

## Routes

All under `http.ServeMux`. CORS-enabled routes wrapped in `corsHandler`.

| Method | Path | Description |
|---|---|---|
| GET | `/metrics` | Go runtime MemStats + custom fields (goroutines, heap, GC, etc) |
| GET | `/api/tracked` | Tracked markets + event/market counts + latest scores |
| GET | `/api/strategies` | Strategy list from `backtest.DefaultFactories()` |
| GET | `/api/simulation` | Pre-computed simulation insights + backtest summaries + cum P&L |
| GET | `/api/paper-orders-insights` | Pre-computed paper order insights + summaries |
| GET | `/api/ticks?event=` | Tick prices + orders for an event (chart data) |
| GET | `/api/orders?limit=&cursor_ts=&cursor_id=` | Paper orders page (cursor pagination) — legacy, replaced by `/api/paper-orders` |
| GET | `/api/paper-orders/meta` | Distinct strategy list (60s cache) |
| GET | `/api/paper-orders/summary?strategies=&min_price=&max_price=&match=&result=` | Per-strategy + total aggregates (server-side GROUP BY) |
| GET | `/api/paper-orders?strategies=&...&cursor_ts=&cursor_id=&limit=` | Paper orders page with server-side filtering + cursor pagination |
| GET | `/api/paper-orders?after_ts=&...` | Delta mode: new orders since afterTS (for polling) |
| GET | `/api/order-counts` | Sim order counts per event |
| GET | `/api/pending-order-counts` | Pending sim order counts per event |
| GET | `/api/passed-matches?limit=` | Recently finalized matches |
| GET | `/api/real-orders` | Real orders (with Kalshi order_id) |
| GET | `/api/liquidity-pool` | Liquidity pool state |
| POST | `/api/liquidity-pool/reset` | Wipe pool, set new balance_cents |
| POST | `/api/liquidity-pool/topup` | Add capital, preserve history |
| GET | `/api/strategy-config` | Per-strategy enable flags |
| PUT | `/api/strategy-config` | Toggle strategy real-trading enable |
| GET | `/api/trigger-ranges?strategy=` | Trigger ranges (all or by strategy) |
| PUT | `/api/trigger-ranges` | Replace all ranges for a strategy |
| GET | `/api/app-config` | All app_config KV pairs |
| PUT | `/api/app-config` | Update app_config key/value |

pprof endpoints (`/debug/pprof/*`) registered via blank import of `net/http/pprof`.

## Conventions

- Read handlers delegate to `LiveStore` (dashboarddata) or `Engine` (backtest).
- Mutation handlers (`PUT`/`POST`) call into `runtimeconfig`, `strategyconfig`, `triggerranges`, `liquiditypool` packages.
- CORS wrapper sets `Access-Control-Allow-Origin: *` + handles `OPTIONS` preflight.
- No auth — server binds to loopback (or LAN-only on mint). Do not expose publicly.

## Gotchas

- `WriteTimeout` is 120s on the outer `http.Server` — long-running backtest recompute on `/api/simulation` first call can take a while.
- `TimeoutHandler` wraps the mux with a 10s deadline. pprof endpoints are registered on the raw mux (exempt). All API routes get 10s — plenty for pre-computed data.
- `Engine` is the backtest replay engine; `LiveStore` is the dashboard's live DB query layer. Don't add live queries to `Engine` — put them in `dashboarddata`.
- `corsHandler` wraps mutation endpoints so the dashboard (port 5173) can call the API (port 6060) without proxy config.
- Dashboard reads use a dedicated `NewDashboardDB` pool (`statement_timeout=5s`, `MaxOpenConns=5`) so slow queries can't block the writer pool.
