# internal/runtimeconfig

Runtime configuration from `app_config` DB table. Owns CRUD methods using `store.RuntimeConfig`/`store.RuntimeConfigHistory` structs.

## Structure

- **`RuntimeConfig`** — all tunable params from `app_config` DB table
- Owns `*gorm.DB` for direct CRUD operations
- Thread-safe via internal `sync.Mutex`
- No dependency on `appconfig` — completely separate
- Imports `store` for `RuntimeConfig`/`RuntimeConfigHistory` struct types only (structs live in store for migration)

## API

- `LoadFromDB(db *gorm.DB) (*RuntimeConfig, error)` — initial load from DB
- `Update(key, value)` — validate, write to DB, reload
- `UpdateBatch(pairs []store.RuntimeConfig)` — validate all, write batch, reload
- `Delete(key)` — remove key, reload
- `GetAll() ([]store.RuntimeConfig, error)` — return all pairs (for dashboard GET)

## Validation

- `validateKey` checks cross-field constraints before DB writes
- Currently: `close_timer_lead_min` ≤ `track_lead_minutes` when `close_timer_enabled`

## app_config DB Keys

- `series_tickers` (JSON array)
- `scan_interval_hours`, `track_lead_minutes`
- `ws_min_backoff_secs`, `ws_max_backoff_secs`
- `batch_size`, `flush_timeout_ms`
- `http_timeout_secs`, `rate_limit_rps`
- `scheduler_poll_secs`
- `apitennis_timezone`
- `kalshi_livedata_enabled`, `kalshi_livedata_poll_secs`
- `close_timer_enabled`, `close_timer_lead_min`, `close_timer_min_price`, `close_timer_poll_secs`, `close_timer_size`
- `reconciler_interval_secs`, `schedule_checker_interval_secs`, `schedule_checker_live_detection`
- `order_quota_enabled`, `order_quota_cooldown_secs`, `order_quota_max_per_sec`
- `order_quota_budget_total`, `order_quota_budget_floor`
- `per_strategy_cooldown_secs`
- `real_trading_enabled`, `kelly_fraction`, `paper_bankroll`, `real_bankroll`
- `real_order_time_in_force`, `real_order_timeout_s`
- `store_raw_payloads` (bool) — gate raw JSON payload storage on WS ingest
- `payload_retention_hours` (int) — janitor nulls payloads older than this (0=disabled)

## Gotchas

- No env config fields here. Env lives in `appconfig.EnvConfig`.
- All mutations must go through `Update`/`UpdateBatch`/`Delete`.
- `app_config` seeded by migration `0002_seed_app_config.sql`.
- DB structs (`RuntimeConfig`, `RuntimeConfigHistory`) live in `store` package for AutoMigrate. CRUD methods live here.
