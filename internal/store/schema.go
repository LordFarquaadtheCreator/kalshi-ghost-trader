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
    last_updated_ts     INTEGER NOT NULL
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
`
