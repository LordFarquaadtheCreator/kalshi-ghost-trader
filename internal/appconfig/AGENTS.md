# internal/appconfig

Technical/environment configuration from YAML files.

## Two-Layer Config

- **This package** — `app.yaml` / `app.dev.yaml`: environment, credentials, paths. Read once at startup.
- **`internal/config`** — `app_config` DB table: runtime tunables. Dashboard-editable.

## File Selection

- `APP_ENV=dev` → `app.dev.yaml`
- `APP_ENV=prod` → `app.yaml`
- Unset → `app.dev.yaml` if it exists, else `app.yaml`

Dev machines keep `app.dev.yaml` and auto-run dev. Prod boxes only have `app.yaml`.

## Fields

- `environment` — "demo" or "prod"
- `kalshi_api_key_id` — Kalshi API key ID
- `kalshi_private_key_path` — path to RSA PEM private key
- `db_path` — SQLite database path (required)
- `metrics_addr` — metrics/pprof bind address (required)
- `apitennis_api_key` — API-Tennis external API key
- `disable_ws_data_save` — skip persisting Kalshi WS ticks/orderbook/lifecycle to DB
- `rest_base_url` — Kalshi REST API base URL (required)
- `ws_url` — Kalshi WebSocket URL (required)
- `backtest_cache_ttl_min` — backtest cache TTL in minutes (required, suggested: 30)

## Gotchas

- `app.yaml` and `app.dev.yaml` are gitignored (contain secrets). Examples committed as `.example`.
- Credentials here override anything in DB. Dashboard cannot change environment or keys.
- `Load()` must be called before DB open (need `db_path`).
