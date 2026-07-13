# internal/tracker

Market subscription lifecycle. No per-match goroutines.

## Model

- `StartMatch` → WS subscribe + add to tracked set
- `StopMatch` → WS unsubscribe + remove from tracked set
- `StopAll` → stop every tracked market

## Design

No per-match goroutine or channel. Ticks stored directly by WS manager's tick writer. Tracker only tracks which markets are subscribed. Kept simple — add per-match logic here if needed in the future.

## Gotchas

- `StartMatch` idempotent. Already-tracked returns nil.
- `StartMatch` rolls back on subscribe error — removes from tracked set.
- `Unsubscribe` sends real Kalshi unsubscribe command with stored sids.
- No `Wait()` — no goroutines to wait for. Shutdown is `StopAll` only.
