-- Convexpool real-order bands: 0.50-0.60 and 0.80-0.90 only.
-- Per cross-day pricebands: 0.50-0.60 (N=899, WR 71.3%, +1668) and
-- 0.80-0.90 (N=1692, WR 93.3%, +842). Other bands net negative or low N.
-- Simulated trades unaffected — bands only gate KalshiOrderEmitter (real orders).
INSERT OR IGNORE INTO strategy_trigger_ranges (strategy, min_price, max_price, source, enabled, created_ts)
VALUES
  ('convexpool', 0.50, 0.60, 'pricebands_2026-07-20', 1, strftime('%s','now') * 1000),
  ('convexpool', 0.80, 0.90, 'pricebands_2026-07-20', 1, strftime('%s','now') * 1000);
