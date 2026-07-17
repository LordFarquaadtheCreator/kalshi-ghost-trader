# internal/apitennis

API-Tennis WebSocket client. Real-time tennis data via `wss://wss.api-tennis.com/live`.

## Files

- `types.go` — WSEvent, SetData, PointData, ScoreData structs
- `matcher.go` — fuzzy player name matching to Kalshi events
- `ws.go` — WebSocket client with auto-reconnect
- `scraper.go` — main Scraper (implements tracker.ScorePoller)

## API

- WS endpoint: `wss://wss.api-tennis.com/live?APIkey=<key>&timezone=<tz>`
- Pushes JSON match updates on every state change (point won, game/set completed)
- No polling. No rate limits on WS.
- REST also available at `api.api-tennis.com/tennis/?method=get_livescore` but WS preferred.

## Data Flow

1. Tracker subscribes markets → calls `StartPolling(eventTicker)`
2. Scraper creates per-match worker goroutine
3. WS pushes match update → worker receives via channel
4. Market registration handled by dispatch via `maybeRegisterMarkets`

## Matching

API-Tennis names: "S. Bejlek", "R. Zarazua"
Kalshi titles: "Bejlek vs Zarazua"
Last-name normalization: lowercase, strip accents, extract last token.

Doubles names: "Furlanetto/Parizzia" — '/' replaced with space before extracting last name. Kalshi doubles titles: "Furlanetto / Parizzia vs Fumagalli / LIUSSO". Both sides of '/' are parsed independently.

## Gotchas

- API-Tennis sends full point-by-point data on every push, not just new points.
- `event_key` is API-Tennis's internal match ID (integer). Cached to avoid repeated name matching.
- Scraper implements `tracker.ScorePoller` interface.
- `event_serve` field: "First Player" = home (server=1), "Second Player" = away (server=2).
- No per-point timestamps from API-Tennis. Uses receive time.
- Doubles player names use '/' separator (e.g. "Furlanetto/Parizzia"). `extractLastName` replaces '/' with space before parsing. Without this, last name extraction fails and doubles events never match.
