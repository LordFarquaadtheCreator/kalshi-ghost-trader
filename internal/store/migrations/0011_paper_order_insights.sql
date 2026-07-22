-- Pre-computed per-strategy × per-day × per-band insights for live paper orders.
-- Source: orders table WHERE is_real = false AND market resolved.
-- Populated by internal/paperorderinsights cron. Read by /api/paper-orders-insights.
CREATE TABLE IF NOT EXISTS paper_order_insights (
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

CREATE INDEX IF NOT EXISTS idx_paper_insights_day
    ON paper_order_insights(day);
CREATE INDEX IF NOT EXISTS idx_paper_insights_strategy
    ON paper_order_insights(strategy);
CREATE INDEX IF NOT EXISTS idx_paper_insights_peak
    ON paper_order_insights(strategy) WHERE peak = true;

-- Per-strategy summary + cumulative P&L series for live paper orders.
-- One row per strategy. cum_pnl_json = ordered [ts, cum_pnl] pairs.
-- Recomputed every cron run (small table, captures new orders without
-- waiting for day rollover).
CREATE TABLE IF NOT EXISTS paper_order_summaries (
    id             bigserial PRIMARY KEY,
    strategy       text UNIQUE NOT NULL,
    run_ts         bigint       NOT NULL,
    total_signals  integer      NOT NULL,
    wins           integer      NOT NULL,
    losses         integer      NOT NULL,
    win_rate       real         NOT NULL,
    total_invested real         NOT NULL,
    net_pnl        real         NOT NULL,
    roi            real         NOT NULL,
    avg_edge       real         NOT NULL,
    sharpe        real         NOT NULL,
    profit_factor real         NOT NULL,
    max_drawdown  real         NOT NULL,
    cum_pnl_json  text         NOT NULL DEFAULT '[]'
);
