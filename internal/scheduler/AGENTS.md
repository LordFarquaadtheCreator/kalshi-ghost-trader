# internal/scheduler

Schedules per-match tracking goroutines at `occurrence_datetime - leadMinutes`.

## Flow

1. Poll DB (interval via `scheduler_poll_secs`, read live each pass) for markets with status 'open' (REST) or 'active' (WS lifecycle)
2. Sort by occurrence_ts
3. Build tracking set from `tracker.ActiveMarkets()` — call ONCE, not per-market
4. Stop tracking markets no longer in DB active set (settled/closed/finalized)
5. For each market not already tracking/pending:
   - If past start time (occurrence_ts - lead): track now
   - If API-Tennis has points for this event (match is live, may have started early): track now
   - Else: spawn cancellable goroutine that waits, then tracks
6. For pending markets (goroutine already scheduled): re-check fresh DB occurrence_ts.
   - If new start time is past OR API-Tennis has points: cancel goroutine, delete pending, track now
   - If new start time differs from scheduled (earlier or later): cancel goroutine, reschedule with new time
   - If unchanged: leave goroutine alone

## Gotchas

- `ActiveMarkets()` returns slice. Convert to set before loop. O(n) not O(n²).
- `TrackLeadMinutes` and `SchedulerPollSecs` read live from `config.Cfg` each pass. Dashboard updates take effect without restart. Ticker `Reset()` on interval change.
- Pending entries are cancellable (`pendingEntry` holds `context.CancelFunc`). Reschedule and track-now paths cancel the stale goroutine. `scheduleOne` delete guarded by `startAt.Equal` check — a rescheduled entry replaces the old one; the cancelled goroutine must not delete the new entry.
- `scheduleOne` verifies market still active via `GetMarket` before subscribing. Walkover/retirement between scheduling and fire time no longer re-subscribes dead markets.
- Market with `occurrence_ts == 0` skipped. Bad data.
- No REST client needed. Reads from DB only (except `scheduleOne`'s `GetMarket` status check).
- Stop-tracking compares tracked set against DB active set. Markets that transition to closed/settled/finalized get unsubscribed.
- Kalshi REST returns `active` for any tradeable market, including future matches days away. `status = 'active'` alone is NOT a signal to track immediately. Only track early if API-Tennis has recorded a point for the event (match is actually live). Without this check, hundreds of future markets track at once, each spawning a kalshilivedata poller, exhausting the rate limiter and starving scanner/reconciler.
- `GetActiveMarkets` includes `determined` status. Markets stay tracked through determination until `settled` arrives. Unsubscribing between `determined` and `settled` drops the settled event — `ApplyLifecycleEvent` never runs, orders never resolve. WS `everTracked` set is the backstop if unsubscribe does happen.
- Pending markets are re-checked each poll with fresh DB occurrence_ts. Schedule checker may move occurrence_ts earlier (rain delay resolved, match moved up) or later (new rain delay). Without re-check, goroutine fires at stale time — either too early (wasted WS slot + poller on a market not yet live) or too late (missed pre-match ticks).
