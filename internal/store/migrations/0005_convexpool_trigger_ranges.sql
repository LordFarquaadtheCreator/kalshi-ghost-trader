-- Convexpool real-order bands: 0.50-0.60 and 0.80-0.90 only.
-- Per cross-day pricebands: 0.50-0.60 (N=899, WR 71.3%, +1668) and
-- 0.80-0.90 (N=1692, WR 93.3%, +842). Other bands net negative or low N.
-- Simulated trades unaffected — bands only gate KalshiOrderEmitter (real orders).
INSERT INTO strategy_trigger_ranges (strategy, min_price, max_price, source, enabled, created_ts)
VALUES
  ('convexpool', 0.50, 0.60, 'pricebands_2026-07-20', true, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('convexpool', 0.80, 0.90, 'pricebands_2026-07-20', true, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT DO NOTHING;
