-- Insights schema: materialized views for dashboard analytics.
-- Every P&L column in cents, every rate as numeric(6,4).

CREATE SCHEMA IF NOT EXISTS insights;

-- strategy_daily: per strategy/day funnel + P&L.
-- Includes one row per (strategy, day, gate_reason) for gated intents
-- plus the funnel counts.
CREATE MATERIALIZED VIEW IF NOT EXISTS insights.strategy_daily AS
SELECT
    o.strategy,
    date_trunc('day', to_timestamp(o.ts_intent / 1000.0))::date AS day,
    o.gate_reason,
    count(*) FILTER (WHERE o.status = 'gated') AS gated_count,
    count(*) FILTER (WHERE o.status = 'accepted') AS accepted_count,
    count(*) FILTER (WHERE o.status IN ('submitted', 'held')) AS submitted_count,
    count(*) FILTER (WHERE o.status IN ('filled', 'partial')) AS filled_count,
    count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0) AS won_count,
    count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents = 0) AS lost_count,
    COALESCE(sum(o.fill_count * o.fill_price_cents) FILTER (WHERE o.status = 'filled'), 0) AS realized_pnl_cents,
    COALESCE(sum(o.fill_count * o.price_cents) FILTER (WHERE o.status IN ('filled', 'partial')), 0) AS invested_cents,
    CASE
        WHEN count(*) FILTER (WHERE o.status IN ('filled', 'partial')) > 0
        THEN count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0)::numeric /
             count(*) FILTER (WHERE o.status IN ('filled', 'partial'))
        ELSE NULL::numeric
    END::numeric(6,4) AS win_rate,
    count(*) AS total_intents
FROM orders_v2 o
GROUP BY o.strategy, date_trunc('day', to_timestamp(o.ts_intent / 1000.0))::date, o.gate_reason
ORDER BY o.strategy, day, o.gate_reason
WITH DATA;

-- Unique index required for CONCURRENTLY refresh.
CREATE UNIQUE INDEX IF NOT EXISTS idx_strategy_daily_key
    ON insights.strategy_daily (strategy, day, COALESCE(gate_reason, ''));

-- band_performance: per strategy/price band fills, hit rate, avg edge, P&L.
CREATE MATERIALIZED VIEW IF NOT EXISTS insights.band_performance AS
SELECT
    o.strategy,
    (o.price_cents / 10) * 10 AS band_cents,
    count(*) FILTER (WHERE o.status IN ('filled', 'partial')) AS fills,
    count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0) AS wins,
    CASE
        WHEN count(*) FILTER (WHERE o.status IN ('filled', 'partial')) > 0
        THEN count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0)::numeric /
             count(*) FILTER (WHERE o.status IN ('filled', 'partial'))
        ELSE NULL::numeric
    END::numeric(6,4) AS hit_rate,
    CASE
        WHEN count(*) FILTER (WHERE o.status IN ('filled', 'partial')) > 0
        THEN avg(o.conv_prob_bps - o.price_cents * 100)::numeric(6,4)
        ELSE NULL::numeric
    END AS avg_edge_bps,
    COALESCE(sum(o.fill_count * o.fill_price_cents) FILTER (WHERE o.status = 'filled'), 0) AS pnl_cents,
    COALESCE(sum(o.fill_count * o.price_cents) FILTER (WHERE o.status IN ('filled', 'partial')), 0) AS invested_cents
FROM orders_v2 o
GROUP BY o.strategy, (o.price_cents / 10) * 10
ORDER BY o.strategy, band_cents
WITH DATA;

CREATE UNIQUE INDEX IF NOT EXISTS idx_band_performance_key
    ON insights.band_performance (strategy, band_cents);

-- match_summary: per event tick count, spread stats, our activity.
CREATE MATERIALIZED VIEW IF NOT EXISTS insights.match_summary AS
SELECT
    o.event_ticker,
    count(DISTINCT o.market_ticker) AS market_count,
    count(*) AS total_orders,
    count(*) FILTER (WHERE o.status = 'gated') AS gated_orders,
    count(*) FILTER (WHERE o.status IN ('filled', 'partial')) AS filled_orders,
    COALESCE(sum(o.fill_count * o.fill_price_cents) FILTER (WHERE o.status = 'filled'), 0) AS realized_pnl_cents,
    min(o.ts_intent) AS first_order_ts,
    max(o.ts_intent) AS last_order_ts
FROM orders_v2 o
GROUP BY o.event_ticker
ORDER BY o.event_ticker
WITH DATA;

CREATE UNIQUE INDEX IF NOT EXISTS idx_match_summary_key
    ON insights.match_summary (event_ticker);

-- pool_equity_curve: daily ledger rollup.
CREATE MATERIALIZED VIEW IF NOT EXISTS insights.pool_equity_curve AS
SELECT
    date_trunc('day', to_timestamp(ts / 1000.0))::date AS day,
    sum(amount_cents) AS delta_cents,
    sum(sum(amount_cents)) OVER (ORDER BY date_trunc('day', to_timestamp(ts / 1000.0))::date) AS cumulative_cents
FROM pool_ledger
GROUP BY date_trunc('day', to_timestamp(ts / 1000.0))::date
ORDER BY day
WITH DATA;

CREATE UNIQUE INDEX IF NOT EXISTS idx_pool_equity_curve_key
    ON insights.pool_equity_curve (day);
