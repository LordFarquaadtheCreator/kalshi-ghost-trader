# internal/scheduler

Schedules per-match tracking goroutines at `occurrence_datetime - leadMinutes`.

## Flow

1. Poll DB every 30s for markets with status 'open' (REST) or 'active' (WS lifecycle)
2. Sort by occurrence_ts
3. Build tracking set from `tracker.ActiveMarkets()` — call ONCE, not per-market
4. Stop tracking markets no longer in DB active set (settled/closed/finalized)
5. For each market not already tracking/pending:
   - If past start time: track now
   - Else: spawn goroutine that waits, then tracks

## Gotchas

- `ActiveMarkets()` returns slice. Convert to set before loop. O(n) not O(n²).
- `scheduleOne` goroutine deletes from `pending` before starting tracking. Race-free — lock protects map.
- Market with `occurrence_ts == 0` skipped. Bad data.
- No REST client needed. Reads from DB only.
- Stop-tracking compares tracked set against DB active set. Markets that transition to closed/settled/finalized get unsubscribed.
