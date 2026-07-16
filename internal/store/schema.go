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
    coverage            TEXT              -- 'full','low_freq','points_only','none' — set at settlement
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

-- FlashScore match mapping: Kalshi event_ticker -> FlashScore match ID
-- One row per mapped match. Updated when match status changes.
CREATE TABLE IF NOT EXISTS flashscore_matches (
    fs_match_id     TEXT PRIMARY KEY,       -- FlashScore internal match ID (AA field)
    event_ticker    TEXT,                   -- Kalshi event_ticker (nullable until mapped)
    home_player     TEXT NOT NULL,
    away_player     TEXT NOT NULL,
    tournament      TEXT,
    surface         TEXT,
    category        TEXT,                   -- ATP, WTA, ITF, Challenger, etc.
    start_ts        INTEGER,                -- match start unix seconds (AD field)
    fs_status       INTEGER,                -- FlashScore stage type (AB field)
    last_polled_ts  INTEGER,
    first_seen_ts   INTEGER NOT NULL,
    last_updated_ts INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_fs_matches_event ON flashscore_matches(event_ticker);
CREATE INDEX IF NOT EXISTS idx_fs_matches_status ON flashscore_matches(fs_status);
CREATE INDEX IF NOT EXISTS idx_fs_matches_start ON flashscore_matches(start_ts);

-- Point-by-point tennis score data from FlashScore
-- No FK to events: points can arrive before event is mapped. Log table.
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
    is_match_point  INTEGER NOT NULL DEFAULT 0,
    is_set_point    INTEGER NOT NULL DEFAULT 0,
    payload         TEXT                    -- raw HL field for debugging
);
CREATE INDEX IF NOT EXISTS idx_points_match_ts ON points(match_ticker, ts_ms);
CREATE INDEX IF NOT EXISTS idx_points_match_set ON points(match_ticker, set_number, game_number, point_number);
CREATE INDEX IF NOT EXISTS idx_points_fs_match ON points(fs_match_id);

-- Simulated orders generated by the match point signal algorithm.
-- No FK to events/markets: orders may be generated before market is stored.
-- Traceable via match_ticker (event_ticker) and market_ticker.
CREATE TABLE IF NOT EXISTS orders (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,       -- when signal fired (ms)
    match_ticker    TEXT NOT NULL,          -- Kalshi event_ticker
    market_ticker   TEXT NOT NULL,          -- Kalshi market_ticker (YES side)
    action          TEXT NOT NULL,          -- "buy"
    context         TEXT NOT NULL,          -- match point context description
    conv_prob       REAL NOT NULL,          -- converted probability (0-1)
    market_price    REAL NOT NULL,          -- market YES price (0-1)
    edge_cents      INTEGER NOT NULL,       -- edge in cents (conv_prob - market_price, in cents)
    suggested_size  REAL NOT NULL,          -- suggested buy size (shares)
    set_number      INTEGER NOT NULL,       -- set when signal fired
    payload         TEXT                    -- extra debug info (JSON)
);
CREATE INDEX IF NOT EXISTS idx_orders_match_ts ON orders(match_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_orders_market ON orders(market_ticker);

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
    UPDATE flashscore_matches SET event_ticker = NULL WHERE event_ticker = OLD.event_ticker;
    DELETE FROM event_lifecycle_events WHERE event_ticker = OLD.event_ticker;
    DELETE FROM points WHERE match_ticker = OLD.event_ticker;
    DELETE FROM orders WHERE match_ticker = OLD.event_ticker;
END;
`
