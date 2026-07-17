# internal/schedulechecker

Polls upcoming matches via REST to detect Kalshi schedule changes.

## Why

Kalshi can update `occurrence_datetime` after market creation (rain delays,
scheduling changes). The scanner runs every 24h — mid-cycle updates are missed.
Strategies cache `occurrence_ts` at registration time and never refresh, so the
pre-match guard in `MultiStrategyRuntime.OnPrice` uses stale data.

## Flow

1. Query `GetUpcomingMarkets` — active markets with `occurrence_ts > now`
2. Deduplicate by `event_ticker` (both markets share the same schedule)
3. REST `GET /markets/{ticker}` for each unique event
4. Compare REST `occurrence_datetime` with DB `occurrence_ts`
5. On change: update DB via `UpsertMarketCheckNew`, call `RefreshOccurrenceTS`
6. `RefreshOccurrenceTS` re-reads from DB and updates `MultiStrategyRuntime` cache

## Config

- `schedule_checker_interval_secs` — poll interval (default: 120)

## Gotchas

- REST rate limiter handles throttling (shared client, 15 RPS default)
- Only fetches one market per event (both share occurrence_ts)
- Skips markets with `occurrence_ts == 0` (bad data)
- No-op when no upcoming markets exist
