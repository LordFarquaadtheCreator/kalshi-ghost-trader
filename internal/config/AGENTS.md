# internal/config

Two-layer config: technical from `app.yaml`, runtime from `app_config` DB table.

## Two-Layer Config

- **`app.yaml` / `app.dev.yaml`** (`internal/appconfig`) — technical config: environment, API keys, DB path, metrics addr. Read once at startup, never changes at runtime. See `internal/appconfig/AGENTS.md`.
- **`app_config` table** (this package) — runtime tunables: intervals, batch sizes, strategy params, bankroll. Dashboard-editable, no restart.

## Loading

- `LoadFromDB(db *store.DB, appCfg *appconfig.AppConfig) (*Config, error)` — merges both layers.
- `ConfigCache` — thread-safe wrapper with `Get()`, `Refresh()`, `Update()`, `UpdateBatch()`.
- Dashboard writes call `Update()` → writes DB + refreshes cache. No restart needed.
- If `app_config` empty → error: "Run migrate-config first".

## app.yaml Fields (technical config)

- `environment` (demo/prod), `kalshi_api_key_id`, `kalshi_private_key_path`
- `db_path`, `metrics_addr`
- `apitennis_api_key`

## app_config DB Keys (runtime tunables)

- `series_tickers` (JSON array)
- `scan_interval_hours`, `track_lead_minutes`
- `ws_min_backoff_secs`, `ws_max_backoff_secs`
- `batch_size`, `flush_timeout_ms`
- `http_timeout_secs`, `rate_limit_rps`
- `scheduler_poll_secs`
- `apitennis_enabled`, `apitennis_timezone`
- `kalshi_livedata_enabled`, `kalshi_livedata_poll_secs`
- `close_timer_enabled`, `close_timer_lead_min`, `close_timer_min_price`, `close_timer_poll_secs`, `close_timer_size`
- `reconciler_interval_secs`, `schedule_checker_interval_secs`
- `order_quota_enabled`, `order_quota_cooldown_secs`, `order_quota_max_per_sec`, `order_quota_daily_limit`
- `order_quota_budget_total`, `order_quota_budget_floor`
- `per_strategy_cooldown_secs`
- `real_trading_enabled`, `kelly_fraction`, `paper_bankroll`, `real_bankroll`
- `real_order_time_in_force`, `real_order_timeout_s`

## Derived Fields

- `RESTBaseURL` — computed from `environment` in `LoadFromDB()`.
- `WSURL` — computed from `environment` in `LoadFromDB()`.

## Gotchas

- Credentials come from app.yaml, not DB. Dashboard cannot change environment or keys.
- `app_config` must be seeded before app starts. Empty table = error.
- `ConfigCache.Refresh()` re-reads entire config from DB + re-applies app.yaml. Call after any `Update()`.
- `series_tickers` stored as JSON array string in DB.
