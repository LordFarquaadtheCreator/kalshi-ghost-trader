-- Seed strategy_config rows for strategies added to backtest.DefaultFactories()
-- after 0002/0009/0012/0016. All disabled by default for real trading.
-- Paper/simulated trades always run (see AGENTS.md global rule).
INSERT INTO strategy_config (strategy, enabled, updated_ts) VALUES
  ('bookpressure', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('bookpressure-strict', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('bookpressure-deep', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('bookpressure-elite', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('convexpool-wta', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('convexpool-exit', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('convexpool-adaptive', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('cross-arb-favorite-itf', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('doublebreak', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setdown-series', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setdown-noon', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-set1', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-set1-mid', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-set12-mid', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-set2', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-set2-ret', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('settlementsniper', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setwinner', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setwinner-aggro', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setwinner-noadjust', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('tiebreak-eu-daytime', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('tiebreak-itfwdoubles', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (strategy) DO NOTHING;
