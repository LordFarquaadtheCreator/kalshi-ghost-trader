# internal/scanner

Daily REST scan of tennis series. Stores new events + markets.

## Flow

1. For each series: `GET /events?series_ticker=X` (all statuses — superset)
2. For each event: upsert event, then `GET /markets?event_ticker=Y`
3. For each market: upsert market
4. Record scan run

## New vs existing

`UpsertEventCheckNew` / `UpsertMarketCheckNew` return `true` if new. Uses `INSERT OR IGNORE` + `RowsAffected()`. Don't use `ON CONFLICT DO UPDATE` — can't distinguish.

## Gotchas

- Tennis = 2 markets per event (one per player). Don't assume 1.
- `player_name` comes from `yes_sub_title`, not title.
- `tennis_competitor` UUID extracted from `custom_strike.tennis_competitor`.
- `occurrence_datetime` = match start. `open_time` = market open. Different.
- Errors per-series don't abort scan. Other series still scanned.
