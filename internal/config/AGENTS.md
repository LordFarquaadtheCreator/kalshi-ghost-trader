# internal/config

Env var loading. All config via `.env` file + environment.

## Vars

- `KALSHI_ENV` — `demo` or `prod`
- `KALSHI_API_KEY_ID` — key ID from Kalshi dashboard
- `KALSHI_PRIVATE_KEY_PATH` — path to RSA PEM file
- `DB_PATH` — SQLite file path
- `SCAN_INTERVAL_HOURS` — scanner poll interval
- `TRACK_LEAD_MINUTES` — start tracking N min before occurrence
- `BATCH_SIZE` — tick insert batch size
- `FLUSH_TIMEOUT_MS` — max wait before flushing batch
- `WS_MIN_BACKOFF_SECS` — reconnect min backoff
- `WS_MAX_BACKOFF_SECS` — reconnect max backoff
- `SERIES_TICKERS` — comma-separated tennis series
- `HTTP_TIMEOUT_SECS` — REST client per-request timeout
- `SCHEDULER_POLL_SECS` — scheduler DB poll interval

## Gotchas

- Demo vs prod URLs differ in host. Don't hardcode.
- Series list must include all 8 core tennis series for full coverage.
- Only credential vars use `KALSHI_` prefix. All others are bare names.
