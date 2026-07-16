# internal/flashscore

FlashScore scraper. Polls internal feed API for tennis point-by-point data.

## Files

- `types.go` — FeedMatch, PointData, MatchPoints, SetPoints
- `client.go` — HTTP client for FlashScore feed API
- `parser.go` — delimited feed parser (¬, ~, ÷ format)
- `matcher.go` — fuzzy player name matching to Kalshi events
- `scraper.go` — main Scraper goroutine (scan loop + poll loop)

## API

FlashScore has no public API. Uses internal feed at `2.flashscore.ninja/2/x/feed/`.
- Region 2 = English locale
- `d.flashscore.com` is geo-blocked from US IPs — must use ninja subdomain
- Auth: `x-fsign: SW9D1eZo` header only. No TLS fingerprinting needed.
- Standard Go `net/http` works. No uTLS/curl_cffi required.

## Endpoints

- `f_2_<day>_1_en_1` — daily match feed. day: -1=today, 0=tomorrow, etc. Sport 2=tennis.
- `df_mh_1_<matchID>` — point-by-point data for a match
- `dc_1_<matchID>` — match metadata + current score

## Feed format

NOT JSON. Delimited text:
- `~` separates records (tournaments, matches, sets, games)
- `¬` separates fields within a record
- `÷` separates key from value

Example: `AA÷p2zdB93F¬AD÷1783845300¬CX÷Bueno G.¬AF÷Marcondes I.¬`

## Point-by-point parsing (df_mh_1)

- `HA÷Set N` — set header
- `HB÷Point by point - Set N` — regular points section
- `HB÷Tiebreak - Set N` — tiebreak section
- `HC÷<home_games> HE÷<away_games>` — game score AFTER this game
- `HG÷<1|2>` — server (1=home, 2=away)
- `HL÷0:15, 0:30, 0:40, 15:40` — point sequence (home:away always)
- Tiebreak: one row per point, HC/HE = TB point count

Game winner derived by comparing HC/HE to previous row (or 0-0 for first game).
Point scorer derived by comparing consecutive scores in HL sequence.
Last point scorer = game winner.

## Name matching

Kalshi event titles: "Muller vs Shevchenko"
FlashScore names: "Muller A." or "Alexandre Muller"

Matching: extract last name from both, normalize (lowercase, strip accents),
find Kalshi event where both players' last names match.

## Gotchas

- No per-point timestamps from FlashScore. Live points use recv_ts as ts_ms.
  Historical backfill: ts_ms is NULL.
- `B1`/`B2` markers in HL field indicate break point conversions — stripped
  during parsing, not separate points.
- Feed returns "0" (single char) for empty/invalid requests. Client treats
  this as no data.
- `fs_status` (AB field in daily feed, AZ field in dc_1): 1=finished,
  2=in-progress, 3=upcoming. Polling targets fs_status=2 plus upcoming
  matches whose start_ts has passed (status may be stale). Status is
  refreshed from dc_1 (AZ field) on every poll cycle.
- Scraper is disabled by default. Set `flashscore_enabled: true` in config.
- Points go through TickWriter's pointsIn channel — same single-writer
  architecture as ticks. No separate writer needed.
- Scraper takes `algorithms.Strategy` interface — calls `OnPoints` after
  ingesting new points, `RegisterMarkets`/`UnregisterMarkets` on match
  start/stop. Same interface in live and backtest.
