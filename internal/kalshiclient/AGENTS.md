# internal/kalshiclient

REST client for Kalshi market data.

## Files

- `client.go` — Client struct, NewClient, signed GET with 429 retry
- `events.go` — GetEvents + event types
- `markets.go` — GetMarkets, GetMarket + market types
- `parse.go` — ParseTennisCompetitor, ParseISOTime, ParseFP

## NewClient

`NewClient(baseURL, signer, httpTimeout, log)`. `httpTimeout=0` uses `defaultHTTPTimeout` (30s). Pass from config (`HTTPTimeoutSecs`).

## Endpoints

- `GET /events?series_ticker=X&status=Y&limit=200&cursor=Z`
- `GET /markets?series_ticker=X&event_ticker=Y&status=Z&limit=1000&cursor=W`
- `GET /markets/{ticker}` — single market

## Pagination

Cursor-based. Empty cursor = done. `limit` max: 200 for events, 1000 for markets.

## Signing

Path passed to signer is `/trade-api/v2` + relative path. Query params stripped.

## Timestamps

Kalshi REST returns ISO-8601 strings. Some have fractional seconds (`2026-07-12T17:37:19.498576Z`), some don't. `ParseISOTime` handles both.

## Gotchas

- Market data endpoints are public (no auth required). We sign anyway for uniformity.
- `custom_strike` is nullable. `json.RawMessage` handles null + object.
- `settlement_ts` is ISO string, not Unix timestamp. Different from WS lifecycle `settled_ts` (seconds).
- `occurrence_datetime` = scheduled match start, not market open time.
