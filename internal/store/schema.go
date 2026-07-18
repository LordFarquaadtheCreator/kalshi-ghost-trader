package store

// schemaDDL is the full SQLite schema. Idempotent — uses CREATE TABLE/INDEX IF NOT EXISTS.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS events (
    event_ticker        TEXT PRIMARY KEY,
    series_ticker       TEXT NOT NULL,
    title               TEXT NOT NULL,
    sub_title           TEXT NOT NULL,
    competition         TEXT,
    competition_scope   TEXT,
    mutually_exclusive  INTEGER,
    first_seen_ts       INTEGER NOT NULL,
    last_updated_ts     INTEGER NOT NULL,
    coverage            TEXT              -- 'full','low_freq','none' — set at settlement
);

CREATE TABLE IF NOT EXISTS markets (
    market_ticker       TEXT PRIMARY KEY,
    event_ticker        TEXT NOT NULL,
    series_ticker       TEXT NOT NULL,
    player_name         TEXT NOT NULL,
    tennis_competitor   TEXT,
    status              TEXT NOT NULL,
    occurrence_ts       INTEGER,
    open_ts             INTEGER,
    close_ts            INTEGER,
    result              TEXT,
    settlement_ts       INTEGER,
    settlement_value    TEXT,
    first_seen_ts       INTEGER NOT NULL,
    last_updated_ts     INTEGER NOT NULL,
    FOREIGN KEY (event_ticker) REFERENCES events(event_ticker)
);
CREATE INDEX IF NOT EXISTS idx_markets_event ON markets(event_ticker);
CREATE INDEX IF NOT EXISTS idx_markets_series ON markets(series_ticker);
CREATE INDEX IF NOT EXISTS idx_markets_status ON markets(status);
CREATE INDEX IF NOT EXISTS idx_markets_occurrence ON markets(occurrence_ts);

-- Every WebSocket message received, stored verbatim + extracted hot fields
-- No FK to markets: ticks are a log, must never be rejected if market
-- not yet stored (race between scanner and WS)
CREATE TABLE IF NOT EXISTS ticks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,   -- server ts_ms from message
    recv_ts         INTEGER NOT NULL,   -- when we received it
    market_ticker   TEXT NOT NULL,
    msg_type        TEXT NOT NULL,      -- "ticker", "trade", etc.
    sid             INTEGER,
    seq             INTEGER,
    price           REAL,
    yes_bid         REAL,
    yes_ask         REAL,
    yes_bid_size    REAL,
    yes_ask_size    REAL,
    volume          REAL,
    open_interest   REAL,
    dollar_volume   INTEGER,
    dollar_open_interest INTEGER,
    last_trade_size REAL,
    trade_id        TEXT,
    no_price        REAL,
    taker_side      TEXT,
    taker_outcome_side TEXT,
    taker_book_side TEXT,
    payload         TEXT NOT NULL       -- full raw JSON
);
CREATE INDEX IF NOT EXISTS idx_ticks_market_ts ON ticks(market_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_ticks_ts ON ticks(ts);
CREATE INDEX IF NOT EXISTS idx_ticks_type ON ticks(msg_type);

-- Market lifecycle changes from WS market_lifecycle_v2 channel
-- No FK: same rationale as ticks — log table, never reject
CREATE TABLE IF NOT EXISTS lifecycle_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,
    recv_ts         INTEGER NOT NULL,
    market_ticker   TEXT NOT NULL,
    event_type      TEXT NOT NULL,      -- "activated", "determined", "settled", etc.
    result          TEXT,
    open_ts         INTEGER,
    close_ts        INTEGER,
    determination_ts INTEGER,
    settled_ts      INTEGER,
    settlement_value TEXT,
    payload         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lifecycle_market ON lifecycle_events(market_ticker, ts);

-- Event lifecycle from WS market_lifecycle_v2 channel (event_lifecycle messages)
CREATE TABLE IF NOT EXISTS event_lifecycle_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,
    recv_ts         INTEGER NOT NULL,
    event_ticker    TEXT NOT NULL,
    series_ticker   TEXT,
    title           TEXT,
    subtitle        TEXT,
    payload         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_event_lifecycle_ticker ON event_lifecycle_events(event_ticker, ts);

-- Orderbook snapshots + deltas from WS orderbook_delta channel
-- No FK: same rationale as ticks — log table, never reject
CREATE TABLE IF NOT EXISTS orderbook_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,   -- server ts_ms (delta) or recv_ts (snapshot)
    recv_ts         INTEGER NOT NULL,
    market_ticker   TEXT NOT NULL,
    msg_type        TEXT NOT NULL,      -- "orderbook_snapshot" or "orderbook_delta"
    sid             INTEGER,
    seq             INTEGER,
    price           REAL,              -- delta only: price level changed
    delta           REAL,              -- delta only: contract delta (signed)
    side            TEXT,              -- delta only: "yes" or "no"
    payload         TEXT NOT NULL       -- full raw JSON
);
CREATE INDEX IF NOT EXISTS idx_orderbook_market_ts ON orderbook_events(market_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_orderbook_type ON orderbook_events(msg_type);

-- Scan runs log
CREATE TABLE IF NOT EXISTS scan_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    run_ts          INTEGER NOT NULL,
    series_ticker   TEXT NOT NULL,
    events_found    INTEGER NOT NULL,
    markets_found   INTEGER NOT NULL,
    new_events      INTEGER NOT NULL,
    new_markets     INTEGER NOT NULL
);

-- Simulated orders generated by the match point signal algorithm.
-- No FK to events/markets: orders may be generated before market is stored.
-- Traceable via match_ticker (event_ticker) and market_ticker.
CREATE TABLE IF NOT EXISTS orders (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,       -- when signal fired (ms)
    match_ticker    TEXT NOT NULL,          -- Kalshi event_ticker
    market_ticker   TEXT NOT NULL,          -- Kalshi market_ticker (YES side)
    match_title     TEXT NOT NULL DEFAULT '', -- human-readable match title
    player_name     TEXT NOT NULL DEFAULT '', -- player name for this market side
    action          TEXT NOT NULL,          -- "buy"
    context         TEXT NOT NULL,          -- match point context description
    conv_prob       REAL NOT NULL,          -- converted probability (0-1)
    market_price    REAL NOT NULL,          -- market YES price (0-1)
    edge_cents      INTEGER NOT NULL,       -- edge in cents (conv_prob - market_price, in cents)
    suggested_size  REAL NOT NULL,          -- suggested buy size (shares)
    set_number      INTEGER NOT NULL,       -- set when signal fired
    strategy        TEXT NOT NULL DEFAULT '', -- which strategy generated this order
    payload         TEXT,                   -- extra debug info (JSON)
    bankroll        REAL NOT NULL DEFAULT 0, -- bankroll used for Kelly sizing
    kelly_fraction  REAL NOT NULL DEFAULT 0, -- Kelly fraction used for sizing
    is_real            INTEGER NOT NULL DEFAULT 0,
    kalshi_order_id    TEXT,
    fill_count         REAL,
    order_status       TEXT,                -- 'submitted','filled','partial','failed','resolved'
    resolved_pnl_cents INTEGER,
    pool_balance_before_cents INTEGER,
    pool_balance_after_cents  INTEGER
);
CREATE INDEX IF NOT EXISTS idx_orders_match_ts ON orders(match_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_orders_market ON orders(market_ticker);
-- idx_orders_real created in post-migration step (store.go) after is_real column added

-- Fired events: tracks which event_tickers a strategy has already fired on.
-- Survives restarts so strategies don't re-fire on the same match.
CREATE TABLE IF NOT EXISTS fired_events (
    event_ticker    TEXT NOT NULL,
    strategy        TEXT NOT NULL,
    fired_ts        INTEGER NOT NULL,
    PRIMARY KEY (event_ticker, strategy)
);

-- Point-by-point score data from FlashScore/API-Tennis scraper.
-- No FK to events: score data may arrive before event is stored.
CREATE TABLE IF NOT EXISTS points (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    match_ticker    TEXT NOT NULL,          -- Kalshi event_ticker
    fs_match_id     TEXT NOT NULL,          -- FlashScore match ID
    ts_ms           INTEGER,                -- unix ms of point (NULL if historical)
    recv_ts         INTEGER NOT NULL,       -- when we stored it
    set_number      INTEGER NOT NULL,       -- which set (1-based)
    game_number     INTEGER NOT NULL,       -- which game within set (1-based)
    point_number    INTEGER NOT NULL,       -- which point within game (1-based)
    server          INTEGER NOT NULL,       -- 1 = home serves, 2 = away serves
    scorer          INTEGER NOT NULL,       -- 1 = home won point, 2 = away won point
    home_points     TEXT NOT NULL,          -- "0", "15", "30", "40", "A"
    away_points     TEXT NOT NULL,
    home_games      INTEGER NOT NULL,       -- games won by home in this set at this point
    away_games      INTEGER NOT NULL,
    home_set_games  INTEGER,                -- final games in completed sets before this one
    away_set_games  INTEGER,
    is_tiebreak     INTEGER NOT NULL DEFAULT 0,
    is_break_point  INTEGER NOT NULL DEFAULT 0,
    is_set_point    INTEGER NOT NULL DEFAULT 0,
    is_match_point  INTEGER NOT NULL DEFAULT 0,
    payload         TEXT                    -- raw HL field for debugging
);
CREATE INDEX IF NOT EXISTS idx_points_match_ts ON points(match_ticker, ts_ms);
CREATE INDEX IF NOT EXISTS idx_points_match_set ON points(match_ticker, set_number, game_number, point_number);
CREATE INDEX IF NOT EXISTS idx_points_fs_match ON points(fs_match_id);

-- App config: key-value store replacing config.yaml
CREATE TABLE IF NOT EXISTS app_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_ts INTEGER NOT NULL
);

-- Liquidity pool: singleton row (id=1), all values in cents
CREATE TABLE IF NOT EXISTS liquidity_pool (
    id                    INTEGER PRIMARY KEY CHECK (id = 1),
    balance_cents         INTEGER NOT NULL,
    initial_balance_cents INTEGER NOT NULL,
    total_spent_cents     INTEGER NOT NULL DEFAULT 0,
    total_pnl_cents       INTEGER NOT NULL DEFAULT 0,
    updated_ts            INTEGER NOT NULL
);

-- Per-strategy real trading config
CREATE TABLE IF NOT EXISTS strategy_config (
    strategy    TEXT PRIMARY KEY,
    enabled     INTEGER NOT NULL DEFAULT 0,
    updated_ts  INTEGER NOT NULL
);

-- Price bands per strategy for real order triggering
CREATE TABLE IF NOT EXISTS strategy_trigger_ranges (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    strategy   TEXT NOT NULL,
    min_price  REAL NOT NULL,
    max_price  REAL NOT NULL,
    source     TEXT NOT NULL DEFAULT 'manual',  -- 'peak' or 'manual'
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_ts INTEGER NOT NULL,
    FOREIGN KEY (strategy) REFERENCES strategy_config(strategy) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_trigger_ranges_strategy ON strategy_trigger_ranges(strategy);

-- Flattened cascade triggers. Delete child rows directly instead of relying
-- on recursive trigger chaining (which requires connection-level PRAGMA).
-- Deletes happen from events outward — markets fire their own cleanup first,
-- then events cleans up everything else in one pass.
CREATE TRIGGER IF NOT EXISTS trg_markets_delete_cascade
AFTER DELETE ON markets
BEGIN
    DELETE FROM ticks WHERE market_ticker = OLD.market_ticker;
    DELETE FROM orderbook_events WHERE market_ticker = OLD.market_ticker;
    DELETE FROM lifecycle_events WHERE market_ticker = OLD.market_ticker;
END;

CREATE TRIGGER IF NOT EXISTS trg_events_delete_cascade
AFTER DELETE ON events
BEGIN
    -- Delete markets first so trg_markets_delete_cascade fires (non-recursive, single-hop)
    DELETE FROM markets WHERE event_ticker = OLD.event_ticker;
    -- Direct child tables not reachable via markets
    DELETE FROM event_lifecycle_events WHERE event_ticker = OLD.event_ticker;
    DELETE FROM orders WHERE match_ticker = OLD.event_ticker;
    DELETE FROM fired_events WHERE event_ticker = OLD.event_ticker;
    DELETE FROM points WHERE match_ticker = OLD.event_ticker;
END;
`
