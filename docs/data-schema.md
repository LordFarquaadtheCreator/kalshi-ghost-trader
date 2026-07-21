# Data Pipeline & Schema

## Data Sources

### Kalshi WebSocket (primary market data)

Real-time market data via WebSocket. Auto-reconnect with exponential backoff.
Stores every ticker, trade, and orderbook message.

### Kalshi REST API (scanner)

Daily REST scan of 12 tennis series. Discovers new events/markets, updates
metadata (status, timestamps, results). Pagination + rate limit handling.

### API-Tennis WebSocket (primary score source)

`wss://wss.api-tennis.com/live?APIkey=<key>&timezone=<tz>`. Auto-reconnects.
Provides real-time point-by-point match updates. Player name matching cached
in-memory. Always enabled; configured via `apitennis_timezone` in app_config.

### Kalshi Live Data REST (backup score source)

REST poller for `/live_data` endpoint. One goroutine per active match.
Provides score snapshots when API-Tennis has no coverage.

## SQLite Tables

WAL mode, synchronous=NORMAL, busy_timeout=5000, cache_size=-64000,
temp_store=MEMORY, foreign_keys=ON. Single writer (MaxOpenConns=1).

### `events` — tennis match events (1 per match)

| Column | Type | Description |
|---|---|---|
| `event_ticker` | TEXT PK | Kalshi event ticker |
| `series_ticker` | TEXT | e.g. KXATPMATCH |
| `title` | TEXT | Match title |
| `sub_title` | TEXT | |
| `competition` | TEXT | e.g. "ATP Masters 1000" |
| `competition_scope` | TEXT | |
| `mutually_exclusive` | BOOL | |
| `first_seen_ts` | INT | When scanner first found it |
| `last_updated_ts` | INT | |
| `coverage` | TEXT | `full` / `low_freq` / `none` (set at settlement) |

### `markets` — 2 per event (one per player)

| Column | Type | Description |
|---|---|---|
| `market_ticker` | TEXT PK | Kalshi market ticker |
| `event_ticker` | TEXT FK | Parent event |
| `series_ticker` | TEXT | |
| `player_name` | TEXT | Player name |
| `tennis_competitor` | TEXT | |
| `status` | TEXT | active, inactive, determined, finalized |
| `occurrence_ts` | INT | Match start time |
| `open_ts` | INT | Market open time |
| `close_ts` | INT | Market close time |
| `result` | TEXT | yes / no |
| `settlement_ts` | INT | |
| `settlement_value` | TEXT | |

### `ticks` — every WS message (ticker, trade)

Log table — no FK. Never reject. Extracted hot fields + raw JSON payload.

| Column | Type | Description |
|---|---|---|
| `id` | INT PK | Auto-increment |
| `ts` | INT | Server unix ms |
| `recv_ts` | INT | Receive time |
| `market_ticker` | TEXT | |
| `msg_type` | TEXT | "ticker", "trade" |
| `sid` / `seq` | INT | Sequence IDs |
| `price` | REAL | Last traded price (0-1) |
| `yes_bid` / `yes_ask` | REAL | Bid-ask spread |
| `yes_bid_size` / `yes_ask_size` | REAL | Depth |
| `volume` | REAL | Cumulative contracts |
| `open_interest` | REAL | Outstanding contracts |
| `dollar_volume` / `dollar_open_interest` | INT | |
| `last_trade_size` | REAL | |
| `trade_id` | TEXT | |
| `no_price` | REAL | |
| `taker_side` / `taker_outcome_side` / `taker_book_side` | TEXT | Trade direction info |
| `payload` | TEXT | Raw JSON (NULLed for non-full coverage at settlement) |

### `orderbook_events` — orderbook snapshots + deltas

No FK. Same reason as ticks.

| Column | Type | Description |
|---|---|---|
| `id` | INT PK | |
| `ts` / `recv_ts` | INT | |
| `market_ticker` | TEXT | |
| `msg_type` | TEXT | "orderbook_snapshot" or "orderbook_delta" |
| `sid` / `seq` | INT | |
| `price` | REAL | Delta: extracted price |
| `delta` | REAL | Delta: size change |
| `side` | TEXT | Delta: "yes" or "no" |
| `payload` | TEXT | Full levels (snapshot) or raw delta |

### `lifecycle_events` — market_lifecycle_v2 WS events

No FK. Tracks market status transitions: activated, deactivated, determined, settled, close_date_updated.

### `event_lifecycle_events` — event_lifecycle WS messages

No FK. Event creation announcements from Kalshi.

### `points` — point-by-point score data from API-Tennis

No FK (may arrive before event stored).

| Column | Type | Description |
|---|---|---|
| `id` | INT PK | |
| `match_ticker` | TEXT | Kalshi event_ticker |
| `fs_match_id` | TEXT | API-Tennis match ID |
| `ts_ms` | INT | Unix ms (may be 0 if historical) |
| `recv_ts` | INT | When stored |
| `set_number` | INT | 1-based |
| `game_number` | INT | 1-based within set |
| `point_number` | INT | 1-based within game |
| `server` | INT | 1 = home, 2 = away |
| `scorer` | INT | 1 = home won point, 2 = away |
| `home_points` / `away_points` | TEXT | "0", "15", "30", "40", "A" (regular) or numeric (tiebreak) |
| `home_games` / `away_games` | INT | Games won in this set at this point |
| `home_set_games` / `away_set_games` | INT | Final games in completed sets |
| `is_tiebreak` | BOOL | Current game is tiebreak |
| `is_break_point` / `is_set_point` / `is_match_point` | BOOL | Point classification flags |
| `payload` | TEXT | Raw JSON |

### `kalshi_scores` — live score snapshots from Kalshi /live_data

PK: `event_ticker`. Backup score source. Point-level granularity via
`points_home`/`points_away` (0/15/30/40/50 where 50=A).

### `orders` — simulated + real orders

No FK. Traceable via `match_ticker` + `market_ticker`.

| Column | Type | Description |
|---|---|---|
| `id` | INT PK | |
| `ts` | INT | Order time |
| `match_ticker` / `market_ticker` | TEXT | |
| `match_title` / `player_name` | TEXT | Populated by real emitter |
| `action` | TEXT | "buy" |
| `context` | TEXT | Strategy context |
| `conv_prob` | REAL | Model probability |
| `market_price` | REAL | Price at order time |
| `edge_cents` | INT | Edge in cents |
| `suggested_size` | REAL | Contracts |
| `set_number` | INT | |
| `strategy` | TEXT | Strategy label |
| `bankroll` | REAL | |
| `kelly_fraction` | REAL | |
| `is_real` | BOOL | Paper or real |
| `kalshi_order_id` | TEXT | Real order ID |
| `fill_count` | REAL | Filled contracts |
| `order_status` | TEXT | |
| `resolved_pnl_cents` | INT | Settled P&L |
| `pool_balance_before_cents` / `pool_balance_after_cents` | INT | Liquidity pool state |
| `unfilled_refunded_cents` | INT | |

### `fired_events` — strategy fire dedup

PK: `(event_ticker, strategy)`. Prevents same strategy firing twice on same match.

### `flashscore_matches` — surface data

PK: `event_ticker`. Surface type for surface-specific strategies.

### `scan_runs` — scan audit log

| Column | Type | Description |
|---|---|---|
| `id` | INT PK | |
| `run_ts` | INT | |
| `series_ticker` | TEXT | |
| `events_found` / `markets_found` | INT | Total discovered |
| `new_events` / `new_markets` | INT | New this scan |

### `app_config` — runtime tunables

KV store with change history (`app_config_history`). Dashboard-editable.
Controls intervals, strategy params, bankroll, lead times.

### `liquidity_pool` — capital tracking

Tracks pool balance for real order sizing.

### `strategy_config` — per-strategy config

Runtime-configurable strategy parameters.

## Why No FK on Log Tables

WS messages can arrive before scanner stores the market. FK would reject the
tick. Data loss. Log tables (ticks, orderbook_events, lifecycle_events,
event_lifecycle_events, points) must never reject.

## Cascade Deletes

Flattened triggers (not recursive FK chains):
- `trg_markets_delete_cascade` — cleans ticks, orderbook, lifecycle on market delete
- `trg_events_delete_cascade` — cleans markets, event_lifecycle, orders, fired_events, points on event delete

Triggers dropped before AutoMigrate (GORM rebuilds tables with _temp copies)
and recreated after.

## Coverage Classification

At settlement, events classified:
- `full` — ≥100 ticks spanning ≥290s in final 5-min pre-close window
- `low_freq` — 1-99 ticks in that window
- `none` — no ticks (auto-pruned on settlement)

Payload retention: non-`full` events have `payload` NULLed in ticks/orderbook
at settlement. Saves disk space.

## Janitor

`CleanOrphans` and `AdoptOrphans` run after each scan cycle. Orphan janitor
removes ticks/points with no parent market. Late-parenting sweep matches
orphaned ticks to markets that arrived in a later scan.

## Migrations

SQL migrations in `migrations/*.sql`, embedded via `go:embed`. Applied in
sorted order on startup. Idempotent. Never edit an applied migration — add
a new one.

- **Changing default/seed data** — new numbered `.sql` file with `INSERT OR IGNORE`
- **Schema changes** — prefer GORM AutoMigrate. Use SQL migrations for indexes, triggers, data seeds.
