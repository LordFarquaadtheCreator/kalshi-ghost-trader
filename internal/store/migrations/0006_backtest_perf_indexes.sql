-- Covering index for backtest tick loading: price + dollar_volume included
-- so Postgres can do index-only scans without heap fetches.
CREATE INDEX IF NOT EXISTS idx_ticks_market_ts_cover
    ON ticks(market_ticker, ts)
    INCLUDE (price, dollar_volume);

-- Partial index: only finalized markets. Backtest/pricebands only query these.
-- Small index (~2500 rows) used as lookup for the IN subquery.
CREATE INDEX IF NOT EXISTS idx_markets_finalized_ticker
    ON markets(market_ticker)
    WHERE status = 'finalized';
