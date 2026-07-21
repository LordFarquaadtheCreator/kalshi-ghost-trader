# internal/config

Global config combining env (app.yaml) and runtime (app_config DB table).

## Structure

- **`Config`** — embeds `*appconfig.EnvConfig` (immutable) + `*runtimeconfig.RuntimeConfig` (mutable). Fields accessed directly: `config.Cfg.RESTBaseURL`, `config.Cfg.SeriesTickers`.
- **`config.Cfg`** — package-level global, set by `Load()`. Access directly.

## API

- `Load(db *store.DB) (*Config, error)` — call once at startup. Loads env from app.yaml, runtime from DB via `db.GormDB()`, sets `Cfg`, returns it.
- No `Get()` — use `config.Cfg` directly.
- No update methods — use `config.Cfg.Update(key, val)` etc. (promoted from embedded `RuntimeConfig`).

## Packages

- `internal/appconfig` — `EnvConfig` struct, YAML loading. Read-only at runtime.
- `internal/runtimeconfig` — `RuntimeConfig` struct, CRUD methods. Uses `store.RuntimeConfig`/`store.RuntimeConfigHistory` for DB structs. No dependency on appconfig.

## Gotchas

- Env fields (credentials, URLs, environment) are read-only. Dashboard cannot change them.
- `app_config` seeded by migration `0002_seed_app_config.sql`. Empty table = migrations not run.
- `series_tickers` stored as JSON array string in DB.
