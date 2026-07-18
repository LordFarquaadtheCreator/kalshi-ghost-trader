# internal/kalshilivedata

Backup live score poller using Kalshi's `/milestones` + `/live_data` REST endpoints.

## Files

- `poller.go` — Poller (implements tracker.ScorePoller), per-match goroutines

## When it runs

Only when `kalshi_livedata_enabled=true` in app_config. API-Tennis remains
primary. This poller fills gaps for matches API-Tennis doesn't cover
(ITF Futures, Davis Cup rubbers, exhibitions).

## API

- `GET /milestones?related_event_ticker=X` → milestone ID for event
- `GET /live_data/milestone/{id}` → live score snapshot

Polled every `kalshi_livedata_poll_secs` (default 10s) per active match.

## Data mapping

Kalshi live_data fields → kalshi_scores table:
- `competitor1_overall_score` / `competitor2_overall_score` → sets won
- `competitor1_round_scores[completed_rounds].score` → games in current set
- `competitor1_current_round_score` → point score (0/15/30/40/50 where 50=A)
- `server` (competitor_id) → 1=home, 2=away
- `completed_rounds` → completed sets
- `status` → "started", "interrupted", "finished"

## Strategy dispatch

OnPoint only dispatched when:
1. Score changed since last poll (dedup)
2. API-Tennis has no points for this match (`HasAPItennisPoints` check)

Synthetic store.Point has `fs_match_id="kalshi-{milestone_id}"` to distinguish
from API-Tennis points. Point-level scores converted to tennis notation
("0"/"15"/"30"/"40"/"A"). Break/set/match point flags computed from game state.

## Storage

`kalshi_scores` table — one row per event, upserted on every poll. Read by
`Engine.LatestScores()` to fill gaps in `/api/tracked` response.

## Lifecycle

- `StartPolling(eventTicker)` — called by tracker on market subscribe
- Resolves milestone ID with retry (may not exist immediately)
- Polls live_data at interval until `StopPolling` or ctx cancelled
- `StopPolling(eventTicker)` — called by tracker on market unsubscribe

## Gotchas

- `current_round_score` is point score (0/15/30/40/50), NOT games. Games come
  from `round_scores[completed_rounds].score`. Got this wrong initially.
- 50 in point score = Advantage ("A")
- Milestone may not exist when event first created — retry with backoff
- Per-match goroutine, not shared poll loop. Each match has its own ticker.
- `MultiScorePoller` in tracker package fans out to both API-Tennis + this.
