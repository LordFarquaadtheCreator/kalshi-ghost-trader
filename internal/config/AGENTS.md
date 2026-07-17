# internal/config

DB-based configuration loading. All config read from `app_config` table in SQLite.

## Loading

- `LoadFromDB(db *store.DB) (*Config, error)` — reads all keys from `app_config`, populates `Config` struct.
- `ConfigCache` — thread-safe wrapper with `Get()`, `Refresh()`, `Update()`, `UpdateBatch()`.
- Dashboard writes call `Update()` → writes DB + refreshes cache. No restart needed.
- If `app_config` empty → error: "Run migrate-config first".
- `DB_PATH` env var sets SQLite path (default: `kalshi_tennis.db`).

## Migration

- `cmd/migrate-config/main.go` — one-time tool. Reads `config.yaml`, seeds `app_config` + `liquidity_pool` + `strategy_config`.
- Run once, then delete `config.yaml`.

## Config Fields (app_config keys)

- `api_key_id`, `private_key_path`, `environment` (demo/prod)
- `series_tickers` (JSON array)
- `scan_interval_hours`, `track_lead_minutes`
- `ws_min_backoff_secs`, `ws_max_backoff_secs`
- `batch_size`, `flush_timeout_ms`
- `http_timeout_secs`, `rate_limit_rps`
- `scheduler_poll_secs`, `metrics_port`
- `apitennis_enabled`, `apitennis_api_key`, `apitennis_timezone`
- `close_timer_enabled`, `close_timer_lead_min`, `close_timer_min_price`, `close_timer_poll_secs`, `close_timer_size`
- `reconciler_interval_secs`, `schedule_checker_interval_secs`
- `order_quota_enabled`, `order_quota_cooldown_secs`, `order_quota_max_per_sec`, `order_quota_daily_limit`
- `order_quota_budget_total`, `order_quota_budget_floor`
- `per_strategy_cooldown_secs`
- `real_trading_enabled`, `kelly_fraction`, `paper_bankroll`, `real_bankroll`
- `real_order_time_in_force`, `real_order_timeout_s`

## Derived Fields

- `RESTBaseURL` — computed from `environment` in `LoadFromDB()`, not stored in DB.
- `WSURL` — computed from `environment` in `LoadFromDB()`, not stored in DB.

## Gotchas

- Demo vs prod URLs differ in host. Don't hardcode.
- `app_config` must be seeded before app starts. Empty table = error.
- `ConfigCache.Refresh()` re-reads entire config from DB. Call after any `Update()`.
- `series_tickers` stored as JSON array string in DB.
