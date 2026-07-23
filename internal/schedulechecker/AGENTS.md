# internal/schedulechecker

Polls upcoming matches via REST to detect Kalshi schedule changes.
Also probes `/milestones` + `/live_data` to detect matches that started
ahead of their scheduled `occurrence_datetime`.

## Why

Two problems:

1. Kalshi can update `occurrence_datetime` after market creation (rain delays,
   scheduling changes). The scanner runs every 24h — mid-cycle updates are missed.
   Strategies cache `occurrence_ts` at registration time and never refresh, so the
   pre-match guard in `MultiStrategyRuntime.OnPrice` uses stale data.

2. Kalshi's `occurrence_datetime` is unreliable for ITF matches. It often points
   to a default future slot (e.g. 20:00 tonight) while the real match is already
   in progress (verified: AHNGRU had occurrence_datetime=20:00 but live_data
   showed set 3 at 14:30). The scanner and scheduler both trust this field, so
   matches that started early are never tracked. `live_data.details.status` is
   the authoritative live signal — "started"/"interrupted" = live, "closed"/
   "complete" = finished. Milestone `details.status` is NOT reliable (shows "P"
   for matches in set 3). Only `live_data` is authoritative.

## Flow

1. Query `GetUpcomingMarkets` — active markets with `occurrence_ts > now`
2. Deduplicate by `event_ticker` (both markets share the same schedule)
3. REST `GET /markets/{ticker}` for each unique event
4. Compare REST `occurrence_datetime` with DB `occurrence_ts`
5. On change: update DB via `UpsertMarketCheckNew`, call `RefreshOccurrenceTS`
6. `RefreshOccurrenceTS` re-reads from DB and updates `MultiStrategyRuntime` cache
7. Live-detection (if `schedule_checker_live_detection=true`):
   - `GET /milestones?related_event_ticker=X` → milestone ID
   - `GET /live_data/milestone/{id}` → live score snapshot
   - If `details.status` is "started" or "interrupted": match is live now
   - Update `occurrence_ts` to `now` via `UpdateOccurrenceTS`
   - Scheduler's next poll (5s) sees `startAt = now - lead` in past → tracks

## Config

- `schedule_checker_interval_secs` — poll interval (default: 120)
- `schedule_checker_live_detection` — enable live_data probing (default: true)

## Gotchas

- REST rate limiter handles throttling (shared client, 15 RPS default)
- Only fetches one market per event (both share occurrence_ts)
- Skips markets with `occurrence_ts == 0` (bad data)
- No-op when no upcoming markets exist
- Live-detection adds 2 REST calls per event per pass (milestones + live_data).
  With ~50 upcoming events deduped, that's ~100 calls/120s = <1 RPS. Fine.
- `checkLive` fails closed: any error, missing milestone, or 404 live_data
  returns false. No false positives — only acts on confirmed "started"/
  "interrupted" status.
- Some events have no milestone (e.g. ANDALV returned empty). Skipped silently.
- Milestone `details.status` is unreliable (stale "P" for live matches). Only
  `live_data.details.status` is authoritative. Do NOT use milestone status.
- Live-detection runs even when REST occurrence_ts matches DB — occurrence can
  be correct but match still started early (Kalshi never updates it).

