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
- `flashscore_enabled` — enable FlashScore scraper (default: false)
- `flashscore_scan_interval_secs` — feed scan interval (default: 300)
- `flashscore_poll_interval_secs` — point poll interval (default: 10)
- `flashscore_lookahead_days` — days to look ahead in feed (default: 1)

### Order quota

- `order_quota_enabled` — throttle order emission (default: true)
- `order_quota_cooldown_secs` — per-market cooldown window (default: 30)
- `order_quota_max_per_sec` — global rate limit, orders/sec (default: 2)
- `order_quota_daily_limit` — hard daily order ceiling, 0 = unlimited (default: 100)
- `paper_budget_total` — paper trading budget in dollars, 0 = no tracking (default: 1000)
- `paper_budget_floor` — stop paper orders when remaining drops below this (default: 50)

### Real trading

- `real_trading_enabled` — submit LIVE orders to Kalshi. DANGEROUS. (default: false)
- `real_max_contracts` — hard cap on contracts per real order (default: 50)
- `real_order_timeout_secs` — per-order HTTP timeout (default: 10)
- `real_budget_total` — real trading budget in dollars, 0 = no tracking (default: 100)
- `real_budget_floor` — stop real orders when remaining drops below this (default: 5)

## Derived Fields

- `RESTBaseURL` — set from environment (demo/prod), not in YAML
- `WSURL` — set from environment (demo/prod), not in YAML

## Gotchas

- Demo vs prod URLs differ in host. Don't hardcode.
- Series list must include all 12 core tennis series for full coverage.
- `CONFIG_PATH` env var overrides default `config.yaml` path.
- `RESTBaseURL` and `WSURL` are derived (yaml:"-"), not user-set.
- `order_quota_enabled` defaults to `true` — quota guard active even in paper mode.
- `real_trading_enabled` defaults to `false` — no real orders unless explicitly enabled.
- Paper and real budgets are independent — both tracked separately in their own `QuotaGuard`.
- `paper_budget_floor` / `real_budget_floor` auto-set to $5 when budget > 0 and floor not specified.
