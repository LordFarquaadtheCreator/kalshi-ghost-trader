package store

// triggerDDL contains SQLite triggers and custom indexes that GORM AutoMigrate
// cannot create. Tables are managed by AutoMigrate from struct definitions.
// This DDL runs after AutoMigrate in migrate().
const triggerDDL = `
CREATE INDEX IF NOT EXISTS idx_markets_event ON markets(event_ticker);
CREATE INDEX IF NOT EXISTS idx_markets_series ON markets(series_ticker);
CREATE INDEX IF NOT EXISTS idx_markets_status ON markets(status);
CREATE INDEX IF NOT EXISTS idx_markets_occurrence ON markets(occurrence_ts);

CREATE INDEX IF NOT EXISTS idx_ticks_market_ts ON ticks(market_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_ticks_ts ON ticks(ts);
CREATE INDEX IF NOT EXISTS idx_ticks_type ON ticks(msg_type);

CREATE INDEX IF NOT EXISTS idx_lifecycle_market ON lifecycle_events(market_ticker, ts);

CREATE INDEX IF NOT EXISTS idx_event_lifecycle_ticker ON event_lifecycle_events(event_ticker, ts);

CREATE INDEX IF NOT EXISTS idx_orderbook_market_ts ON orderbook_events(market_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_orderbook_type ON orderbook_events(msg_type);

CREATE INDEX IF NOT EXISTS idx_orders_match_ts ON orders(match_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_orders_market ON orders(market_ticker);

CREATE INDEX IF NOT EXISTS idx_points_match_ts ON points(match_ticker, ts_ms);
CREATE INDEX IF NOT EXISTS idx_points_match_set ON points(match_ticker, set_number, game_number, point_number);
CREATE INDEX IF NOT EXISTS idx_points_fs_match ON points(fs_match_id);

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
