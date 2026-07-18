# internal/tracker

Market subscription lifecycle. No per-match goroutines.

## Model

- `StartMatch` → WS subscribe + add to tracked set + start score polling
- `StopMatch` → WS unsubscribe + remove from tracked set + stop score polling
- `StopAll` → stop every tracked market

## ScorePoller

Optional interface for score data sources. `StartPolling`/`StopPolling` called
on first market subscribe / last market unsubscribe per event. Wired via
`MultiScorePoller` to fan out to both API-Tennis + Kalshi live-data.

## Design

No per-match goroutine or channel. Ticks stored directly by WS manager's tick writer. Tracker only tracks which markets are subscribed. Kept simple — add per-match logic here if needed in the future.

## Gotchas

- `StartMatch` idempotent. Already-tracked returns nil.
- `StartMatch` rolls back on subscribe error — removes from tracked set.
- `Unsubscribe` sends real Kalshi unsubscribe command with stored sids.
- No `Wait()` — no goroutines to wait for. Shutdown is `StopAll` only.
