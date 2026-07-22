-- 0008_positions_table.sql
-- Positions table for sell-to-close pipeline. One row per
-- (market_ticker, strategy, is_real). Aggregates buys + sells.
-- Backward compat: existing buy-only orders have no position row;
-- reconciler's legacy ResolveRealOrders/ResolveSimulatedOrders still
-- handles them. New sell-aware path activates only when a position row
-- exists (i.e. an order with side='close' triggered position creation).

-- side column on orders. NULL = legacy buy (treated as 'open' for new path).
ALTER TABLE orders ADD COLUMN IF NOT EXISTS side TEXT;
-- position_id links an order to its position (NULL for legacy orders).
ALTER TABLE orders ADD COLUMN IF NOT EXISTS position_id BIGINT;

CREATE TABLE IF NOT EXISTS positions (
    id BIGSERIAL PRIMARY KEY,
    match_ticker TEXT NOT NULL,
    market_ticker TEXT NOT NULL,
    strategy TEXT NOT NULL,
    is_real BOOLEAN NOT NULL DEFAULT false,
    filled_buy_count NUMERIC(20,4) NOT NULL DEFAULT 0,
    filled_sell_count NUMERIC(20,4) NOT NULL DEFAULT 0,
    avg_entry_price NUMERIC(10,4) NOT NULL DEFAULT 0,
    avg_exit_price NUMERIC(10,4) NOT NULL DEFAULT 0,
    realized_pnl_cents BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'open',
    opened_ts BIGINT NOT NULL DEFAULT 0,
    closed_ts BIGINT NOT NULL DEFAULT 0,
    UNIQUE(match_ticker, market_ticker, strategy, is_real)
);

CREATE INDEX IF NOT EXISTS idx_positions_open
    ON positions(market_ticker) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_positions_match
    ON positions(match_ticker, strategy);
