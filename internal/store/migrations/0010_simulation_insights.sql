-- Pre-computed per-strategy × per-day × per-band insights.
-- Populated by the pricebands cron alongside price_band_results.
-- Replaces live ComputePriceBands calls on /strategies + /price-bands.
CREATE TABLE IF NOT EXISTS simulation_insights (
    id            bigserial PRIMARY KEY,
    run_ts        bigint       NOT NULL,
    day           text         NOT NULL,
    strategy      text         NOT NULL,
    band_label    text         NOT NULL,
    band_lo       real         NOT NULL,
    band_hi       real         NOT NULL,
    n             integer      NOT NULL,
    wins          integer      NOT NULL,
    win_rate      real         NOT NULL,
    net_pnl       real         NOT NULL,
    invested      real         NOT NULL,
    roi           real         NOT NULL,
    avg_edge      real         NOT NULL,
    sharpe        real         NOT NULL,
    profit_factor real         NOT NULL,
    max_drawdown  real         NOT NULL,
    score         real         NOT NULL,
    peak          boolean      NOT NULL DEFAULT false,
    UNIQUE (strategy, day, band_label)
);

CREATE INDEX IF NOT EXISTS idx_sim_insights_day
    ON simulation_insights(day);
CREATE INDEX IF NOT EXISTS idx_sim_insights_strategy
    ON simulation_insights(strategy);
CREATE INDEX IF NOT EXISTS idx_sim_insights_peak
    ON simulation_insights(strategy) WHERE peak = true;

-- Pre-computed cumulative P&L series per strategy (global, all time).
-- Ordered [ts, cum_pnl] pairs. Much smaller than shipping full orders_json.
ALTER TABLE backtest_results ADD COLUMN IF NOT EXISTS cum_pnl_json text;
