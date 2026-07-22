-- R.1: denormalize market result onto orders so the paper-orders route can
-- filter and aggregate without joining markets. settled_ts mirrors the
-- market's close_ts (or settlement_ts when set) at backfill time.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS result text;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS settled_ts bigint;

UPDATE orders o
SET result = m.result,
    settled_ts = COALESCE(m.settlement_ts, m.close_ts)
FROM markets m
WHERE o.market_ticker = m.market_ticker
  AND m.result IS NOT NULL AND m.result <> ''
  AND (o.result IS NULL OR o.result = '');

CREATE INDEX IF NOT EXISTS idx_orders_strategy_ts
  ON orders(strategy, ts DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_orders_unsettled_ts
  ON orders(ts) WHERE result IS NULL OR result = '';
