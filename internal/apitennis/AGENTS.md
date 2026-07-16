# internal/apitennis

API-Tennis WebSocket client. Real-time point-by-point tennis data via `wss://wss.api-tennis.com/live`.

## Files

- `types.go` — WSEvent, SetData, PointData, ScoreData structs
- `matcher.go` — fuzzy player name matching to Kalshi events
- `ws.go` — WebSocket client with auto-reconnect
- `scraper.go` — main Scraper (implements tracker.FlashScorePoller)

## API

- WS endpoint: `wss://wss.api-tennis.com/live?APIkey=<key>&timezone=<tz>`
- Pushes JSON match updates on every state change (point won, game/set completed)
- No polling. No rate limits on WS.
- REST also available at `api.api-tennis.com/tennis/?method=get_livescore` but WS preferred.

## Data Flow

1. Tracker subscribes markets → calls `StartPolling(eventTicker)`
2. Scraper registers markets with signal generator
3. WS pushes match update → `processEvent` matches to Kalshi event
4. Diffs points against stored count → ingests new ones via TickWriter
5. Signal generator processes points → fires orders at match points

## Matching

API-Tennis names: "S. Bejlek", "R. Zarazua"
Kalshi titles: "Bejlek vs Zarazua"
Same last-name normalization as FlashScore matcher.

## Gotchas

- API-Tennis sends full point-by-point data on every push, not just new points. Scraper diffs by comparing total point count vs stored.
- `event_key` is API-Tennis's internal match ID (integer). Cached to avoid repeated name matching.
- Scraper implements `FlashScorePoller` interface — can replace FlashScore or run alongside it (but only one should be active per event to avoid duplicate points).
- `event_serve` field: "First Player" = home (server=1), "Second Player" = away (server=2).
- No per-point timestamps from API-Tennis. `ts_ms` uses receive time, same as FlashScore.
- Match point / break point flags come from API-Tennis directly (`match_point`, `break_point` fields).
