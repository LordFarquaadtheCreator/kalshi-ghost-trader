-- Add cascade triggers for foreign key behavior
-- These are safe where FKs aren't possible (log tables) and supplement the
-- existing FK on markets -> events which lacks ON DELETE CASCADE.

-- When an event is deleted, cascade to markets and related tables
CREATE TRIGGER IF NOT EXISTS trg_events_delete_cascade
AFTER DELETE ON events
BEGIN
    -- Cascade to markets (FK exists but no ON DELETE CASCADE)
    DELETE FROM markets WHERE event_ticker = OLD.event_ticker;
    -- Set null on flashscore mapping (event_ticker is nullable)
    UPDATE flashscore_matches SET event_ticker = NULL WHERE event_ticker = OLD.event_ticker;
    -- Clean up lifecycle events for this event
    DELETE FROM event_lifecycle_events WHERE event_ticker = OLD.event_ticker;
    -- Points reference the event as match_ticker
    DELETE FROM points WHERE match_ticker = OLD.event_ticker;
END;

-- When a market is deleted, cascade to its log tables
CREATE TRIGGER IF NOT EXISTS trg_markets_delete_cascade
AFTER DELETE ON markets
BEGIN
    DELETE FROM ticks WHERE market_ticker = OLD.market_ticker;
    DELETE FROM orderbook_events WHERE market_ticker = OLD.market_ticker;
    DELETE FROM lifecycle_events WHERE market_ticker = OLD.market_ticker;
END;
