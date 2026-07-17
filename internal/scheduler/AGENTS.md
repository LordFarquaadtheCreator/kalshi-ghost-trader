# internal/scheduler

Schedules per-match tracking goroutines at `occurrence_datetime - leadMinutes`.

## Flow

1. Poll DB every 5s (configurable via `scheduler_poll_secs`) for markets with status 'open' (REST) or 'active' (WS lifecycle)
2. Sort by occurrence_ts
3. Build tracking set from `tracker.ActiveMarkets()` — call ONCE, not per-market
4. Stop tracking markets no longer in DB active set (settled/closed/finalized)
5. For each market not already tracking/pending:
   - If status is 'active' (WS lifecycle confirmed live): track immediately, regardless of occurrence_ts
   - If past start time (occurrence_ts - lead): track now
   - Else: spawn goroutine that waits, then tracks
6. For pending markets (goroutine already scheduled): re-check fresh DB occurrence_ts. If schedule checker moved it earlier and new start time is past, track now. Old goroutine fires later as no-op (StartMatch is idempotent).

## Gotchas

- `ActiveMarkets()` returns slice. Convert to set before loop. O(n) not O(n²).
- `scheduleOne` goroutine deletes from `pending` before starting tracking. Race-free — lock protects map.
- Market with `occurrence_ts == 0` skipped. Bad data.
- No REST client needed. Reads from DB only.
- Stop-tracking compares tracked set against DB active set. Markets that transition to closed/settled/finalized get unsubscribed.
- `status = 'active'` means Kalshi WS sent a market_lifecycle "activated" event — market is live NOW, even if scheduled occurrence_ts is in the future. Start tracking immediately.
- Pending markets are re-checked each poll with fresh DB occurrence_ts. Schedule checker may move occurrence_ts earlier (rain delay resolved, match moved up). Without re-check, goroutine fires at stale time.
