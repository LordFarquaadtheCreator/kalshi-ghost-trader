# internal/dashboarddata

Live DB queries for the dashboard API. Extracted from `internal/backtest` so
the backtest package is replay-only and the dashboard has its own data layer
that does not reach through `Engine`.

## Files

- `store.go` — `LiveStore` type, all dashboard query methods
- `store_test.go` — tests

## LiveStore

```go
type LiveStore struct {
    db          *gorm.DB
    log         *slog.Logger
    eventTitles map[string]string   // cached at construction
}
```

`NewLiveStore(db, log)` loads event titles once for cheap lookup in hot paths
(`trackedHandler`). Callers reuse the `store.DB` gorm handle — do not open a
second connection.

## Methods

- `EventTitle(eventTicker) string` — cached lookup
- `EventOccurrenceTS(ctx, eventTickers) map[string]int64` — bulk fetch
- `LatestTickTS(ctx, eventTickers) map[string]int64` — last tick per event
- `LatestScores(ctx, eventTickers) map[string]*LiveScore` — joins `points` (primary) + `kalshi_scores` (backup). Returns nil for events with no score data.
- `GetEventTickPrices(ctx, eventTicker) *EventTickData` — tick prices + sim orders for chart rendering
- `GetPaperOrderSummary(ctx) PaperOrderSummary` — aggregate stats across all paper orders
- `GetPaperOrderStrategies(ctx) []string` — distinct strategy labels
- `GetPaperOrdersPage(ctx, cursor, limit) ([]PaperOrder, bool, *PaperOrderCursor, error)` — cursor pagination (cursor_ts + cursor_id)
- `GetOrderCountsByEvent(ctx) map[string]int` — sim order counts
- `GetPendingOrderCountsByEvent(ctx) map[string]int` — pending sim orders
- `GetPassedMatches(ctx, limit) []PassedMatch` — recently finalized matches

## Types

- `LiveScore` — `{EventTicker, HomeScore, AwayScore, Server, IsTiebreak, Source}`. `Source` is `"apitennis"` or `"kalshi"`.
- `EventTickData` — `{EventTicker, Title, Markets: [{MarketTicker, PlayerName, Ticks: [{TS, Price}]}], Orders: [...]}`
- `PaperOrder` — full order row + computed fields for dashboard
- `PaperOrderCursor` — `{TS, ID}` for keyset pagination
- `PaperOrderSummary` — aggregate counts + P&L
- `PassedMatch` — finalized match with result + coverage

## Gotchas

- `LatestScores` falls back from `points` (API-Tennis, primary) to `kalshi_scores` (Kalshi live-data, backup) per event. Events with neither return nil — dashboard shows them as "upcoming" not "live".
- Event titles cached at `NewLiveStore` construction. New events added after startup won't appear in cache until restart. Acceptable — dashboard polls `/api/tracked` which uses the cache for already-known events; new events get their title from the `events` table via `GetEventTickPrices`.
- Cursor pagination uses `(ts, id)` keyset, not OFFSET. Stable under concurrent inserts.
- Read-only. No writes here. All mutations go through `runtimeconfig`/`strategyconfig`/`triggerranges`/`liquiditypool`.
