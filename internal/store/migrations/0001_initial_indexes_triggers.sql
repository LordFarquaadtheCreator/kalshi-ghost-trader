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

CREATE INDEX IF NOT EXISTS idx_orders_real ON orders(is_real) WHERE is_real = true;
CREATE INDEX IF NOT EXISTS idx_orders_ts_id ON orders(ts DESC, id DESC);

-- Cascade triggers are created in store.go Migrate() using PL/pgSQL.
