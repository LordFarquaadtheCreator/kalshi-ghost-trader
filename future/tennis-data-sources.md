# Tennis Data Sources — Research for Better ITF Coverage

**Date:** 2026-07-23
**Context:** Current `internal/apitennis` scraper (API-Tennis WebSocket) covers only ~33% of
finalized Kalshi events with point data. ITF and doubles series have near-zero coverage.
This document evaluates alternative data sources to fill the gap.

## Problem

The `setpoint` / `matchpoint-aggro` / `setpoint-serve` strategies need **point-by-point (PBP)
data** to compute Markov match-win probabilities and detect set/break points. The current
API-Tennis source:

- Covers ~698 / 2119 finalized Kalshi events with point data (~33%).
- ITF and doubles series have very low coverage.
- 353 events had points but no ticks (ingestion gap, separate issue).

We need a source with **broader ITF coverage** and **real-time PBP**.

## Sources Evaluated

### 1. Matchstat / Tennis-API (RapidAPI) — RECOMMENDED

- **URL:** https://rapidapi.com/jjrm365-kIFr3Nx_odV/api/tennis-live-api
- **Docs:** https://tennisapidoc.matchstat.com/
- **Coverage:** ATP, WTA, Challenger, ITF M15/M25 men, ITF W15-W100 women, doubles.
  ITF events are accessed via `tourType=atp|wta` with `rankId=0` (ITF $10K) or
  `rankId=1` (Challenger / ITF >$10K). There is **no `itf` tourType** — passing it
  returns a 400 error.
- **PBP endpoint:** `GET /tennis/v2/extend/api/event/pbp/{player1Id}/{player2Id}/{tourId}/{roundId}`
  - Returns per-game data: `server`, `tiebreak`, `finalScore`, `gameNumber`,
    `fifteens_content` (e.g. `"15:0, 30:0, 40:0"`).
  - This is exactly the granularity needed for set-point / break-point detection.
- **Live events:** `GET /tennis/v2/extend/api/events/live` — returns all live matches
  with `id`, `name`, `participant1/2`, `league`, `score`, `status`, `points`,
  `indicator`, `tourType`, `startTimestamp`, `matchId`.
  - Example live response included a **W15 Mogyorod** ITF match — confirms ITF live coverage.
- **Real-time:** Socket.IO push (`wss://live.matchstat.com`) on Mega plan.
  - Events: `join-event` / `leave-event` / `event-update` (score, sets, server, status).
  - All-matches subscription: `join-live-events-all` with `sportSlug="tennis"`.
  - Token auth required via `/ws-token` endpoint (Mega only).
- **Pricing (verified via BrowserClaw on RapidAPI pricing page):**
  | Plan | Price | Requests | Rate Limit |
  |------|-------|----------|------------|
  | Basic | $0/mo | 50/day | 4 req/s |
  | Pro | $29/mo | 200K/mo | 10 req/s |
  | Ultra | $59/mo | 1.2M/mo | 25 req/s |
  | Mega | $99/mo | 3.5M/mo | 50 req/s |
  - Socket.IO requires **Mega ($99/mo)**.
  - PBP + live odds require **Ultra ($59/mo)** or higher.
- **Match ID format:** `{player1Id}-{player2Id}-{tourId}-{roundId}` (e.g. `45191-59913-17112-12`).
- **Player names:** Full names ("Adrienn Nagy", "Kamil Majchrzak"). Same last-name
  normalization as current `matcher.go` applies.
- **Bookmakers:** Includes Bet365, Pinnacle, Polymarket, DraftKings, etc. (20+ books).
- **Risks:**
  - ITF PBP depth unverified end-to-end (docs claim it; the live-events example
    included a W15 ITF match, which is promising). Free Basic tier (50 req/day)
    allows verification before paying.
  - RapidAPI vendor dependency.
  - Socket.IO token auth adds complexity vs. current raw WebSocket.

### 2. SofaScore (Unofficial API) — FALLBACK

- **URL:** https://www.sofascore.com/tennis/itf-men
- **API base:** `https://api.sofascore.com/api/v1/` (mirror: `api.sofascore.app`)
- **Coverage:** ITF M15/M25 men, ITF W15-W100 women, doubles — all visible on the
  website with live set/game/point scores. Verified via BrowserClaw: live ITF M15
  matches (Bali, Brisbane, Huamantla, Kursumlijska Banja, Rognac, Segrate, Champaign)
  showing set+game+point scores in real time.
- **PBP endpoint:** `GET /api/v1/event/{eventId}/point-by-point` (tennis only).
- **Real-time:** REST polling only (no official WebSocket). Polling cadence ~2-5s.
- **Cost:** Free (unofficial scraping).
- **Blocking issues (verified):**
  - **Cloudflare WAF** blocks standard HTTP clients (curl returns 403).
    Requires `curl_cffi` TLS fingerprint impersonation (Python) or a headless
    browser. Go has no native equivalent — would need a Python sidecar or
    browser-based proxy.
  - **API returns 404 from direct browser fetch** — `api.sofascore.com` and
    `api.sofascore.app` both returned 404/empty when fetched directly via
    BrowserClaw without proper `X-Requested-With` header and referer. The
    `www.sofascore.com/api` proxy path also 404'd.
  - **ToS violation** — unofficial, can break anytime, no SLA.
  - Existing community clients (`pseudo-r/Public-Sofascore-API`,
    `Kirill52300/sofascore_api`) all use `curl_cffi` with Chrome impersonation.
- **Verdict:** High coverage, zero cost, but high maintenance burden and
  fragility. Only viable as a fallback with a Python sidecar.

### 3. Sportradar Tennis v2 — DEAD END

- **URL:** https://developer.sportradar.com/tennis/docs/ig-data-coverage-tiers
- **ITF coverage dropped in 2025:** "Starting in 2025, the ITF World Tennis Tour
  will no longer be a part of the Tennis API."
- Tier 5 (ITF, up to 2024 only) had PBP from round 1 via ITF umpire SR devices.
  Now removed.
- Davis Cup / Billie Jean King Cup still covered.
- **Cost:** Enterprise sales call, 30-day free trial available.
- **Verdict:** No ITF = useless for our gap.

### 4. api4sports — UNVERIFIED

- **URL:** https://www.api4sports.com/products/tennis-api
- **Coverage:** Claims Grand Slams, ATP/WTA tours, Challengers, ITF with PBP
  (serve outcomes, aces, double faults, winners/errors, breaks, tiebreaks).
- **Endpoints:** `/tennis/live`, `/tennis/matches/{matchId}/details` (full PBP).
- **Real-time:** REST polling only. No WebSocket.
- **Cost:** Paid (free tier for testing).
- **Verdict:** Possible alternative but coverage claims unverified. REST-only
  means higher latency than Socket.IO push.

### 5. Flashscore (Apify scraper) — USELESS

- **URL:** https://apify.com/extractify-labs/flashscore-tennis-matches
- **Coverage:** ATP, WTA, ITF, Challenger listings.
- **Data depth:** Set-by-set scores + current game score + server. **No PBP.**
- **Cost:** ~$0.01/run on Apify Free tier.
- **Verdict:** No point-by-point data — does not meet requirements.

### 6. DataSportsGroup — ENTERPRISE

- **URL:** https://datasportsgroup.com/coverage/tennis/
- **Coverage:** ATP, WTA, ITF, Grand Slams with live PBP.
- **Cost:** Enterprise sales call only.
- **Verdict:** Likely good coverage but opaque pricing and procurement overhead.

## Recommendation

**Primary: Matchstat Tennis Live API (Mega plan, $99/mo)**

Reasoning:
1. **Explicit ITF PBP coverage** — docs list ITF M15/M25 and W15-W100. Live-events
   example response included a W15 ITF match.
2. **Socket.IO push** — same push model as current API-Tennis. Server pushes on
   every point. No polling.
3. **PBP granularity matches our needs** — per-game `fifteens_content` with server
   and finalScore enables set-point / break-point detection.
4. **Cheapest option with real ITF PBP + WS** at $99/mo.
5. **Same architecture** — implements `tracker.ScorePoller`. Drop-in replacement
   for `apitennis.Scraper`. New package `internal/matchstat/`, same interface.

**Fallback: SofaScore unofficial API (free, Python sidecar)**

If Matchstat's ITF PBP depth turns out to be shallow after testing, fall back to
SofaScore scraping via a Python sidecar using `curl_cffi`. Higher maintenance but
free and broad ITF coverage.

## Integration Plan

### Phase 1: Verify (30 min, free)
1. Subscribe to Matchstat Basic tier (free, 50 req/day).
2. Fetch `/events/live` during an ITF M15 match window.
3. Fetch `/event/pbp/{p1Id}/{p2Id}/{tourId}/{roundId}` for a completed ITF match.
4. Confirm `fifteens_content` is populated (not empty) for ITF games.

### Phase 2: Build `internal/matchstat/` package
1. `types.go` — LiveEvent, PBPResponse, Game, Set structs.
2. `matcher.go` — fuzzy player name matching to Kalshi events (reuse
   `apitennis.matcher` logic; full names → last-name normalization).
3. `ws.go` — Socket.IO client with token auth (`/ws-token` endpoint) and
   auto-reconnect. Subscribe via `join-live-events-all` for `sportSlug="tennis"`.
4. `scraper.go` — main Scraper implementing `tracker.ScorePoller`.
   - On `StartPolling(eventTicker)`: resolve event ID, emit `join-event`.
   - On `event-update`: parse score/sets/server, feed to strategy.
   - On `StopPolling`: emit `leave-event`.
5. Wire via `MultiScorePoller` alongside API-Tennis as fallback:
   ```
   multi := NewMultiScorePoller(matchstatScraper, apitennisScraper)
   ```

### Phase 3: Cutover
1. Deploy with both scrapers active (Matchstat primary, API-Tennis fallback).
2. Monitor coverage: query `points` table for ITF events with point data.
3. If Matchstat coverage > API-Tennis, drop API-Tennis.

## Open Questions

- Does Matchstat populate `fifteens_content` for ITF M15 (lowest tier), or only
  for higher ITF levels (M25, W100)? Needs Phase 1 verification.
- Does the Socket.IO `event-update` push include per-point granularity, or only
  game/set-level changes? If only game-level, we'd need to poll `/event/pbp`
  on each `event-update` to get point progression.
- Matchstat `matchId` format `{p1Id}-{p2Id}-{tourId}-{roundId}` — how does this
  map to Kalshi event tickers? Need a name-based match (same as current).

## Sources

- Matchstat RapidAPI pricing (verified via BrowserClaw 2026-07-23)
- Matchstat docs: https://tennisapidoc.matchstat.com/extend-endpoints
- Matchstat Socket.IO: https://tennisapidoc.matchstat.com/socket-integration
- Sportradar coverage tiers: https://developer.sportradar.com/tennis/docs/ig-data-coverage-tiers
- Sportradar 2025 coverage update: https://developer.sportradar.com/sportradar-updates/changelog/tennis-api-coverage-updates
- SofaScore ITF men live (verified via BrowserClaw 2026-07-23): https://www.sofascore.com/tennis/itf-men
- SofaScore unofficial API docs: https://github.com/pseudo-r/Public-Sofascore-API
- api4sports: https://www.api4sports.com/products/tennis-api
- Flashscore Apify: https://apify.com/extractify-labs/flashscore-tennis-matches
- DataSportsGroup: https://datasportsgroup.com/coverage/tennis/
