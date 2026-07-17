# internal/config

YAML-based configuration loading. All config via `config.yaml` (or `CONFIG_PATH` env var override).

## Config Fields

- `api_key_id` — Kalshi API key ID
- `private_key_path` — path to RSA PEM private key
- `environment` — `demo` or `prod` (determines REST/WS URLs)
- `db_path` — SQLite file path (default: `kalshi_tennis.db`)
- `series_tickers` — list of tennis series to scan (defaults to all 12 core series: 8 singles + 4 doubles)
- `scan_interval_hours` — scanner poll interval (default: 24)
- `track_lead_minutes` — start tracking N min before occurrence (default: 5)
- `ws_min_backoff_secs` / `ws_max_backoff_secs` — reconnect backoff range (default: 1–30)
- `batch_size` — tick insert batch size (default: 500)
- `flush_timeout_ms` — max wait before flushing batch (default: 250)
- `http_timeout_secs` — REST client per-request timeout (default: 30)
- `rate_limit_rps` — REST client max requests per second (default: 15)
- `scheduler_poll_secs` — scheduler DB poll interval (default: 30)
- `metrics_port` — pprof + runtime metrics HTTP server port (default: 6060, 0 = disabled)
- `apitennis_enabled` — enable API-Tennis WebSocket scraper (default: false)
- `apitennis_api_key` — API-Tennis API key (required if enabled)
- `apitennis_timezone` — timezone for API-Tennis requests (default: +00:00)

## Derived Fields

- `RESTBaseURL` — set from environment (demo/prod), not in YAML
- `WSURL` — set from environment (demo/prod), not in YAML

## Gotchas

- Demo vs prod URLs differ in host. Don't hardcode.
- Series list must include all 12 core tennis series for full coverage.
- `CONFIG_PATH` env var overrides default `config.yaml` path.
- `RESTBaseURL` and `WSURL` are derived (yaml:"-"), not user-set.
