-- Seed cross-arb-favorite strategy config. Disabled by default for real trading.
-- Paper/simulated trades always run (see AGENTS.md global rule).
-- Directional fade of overpriced favorite: when yesSum > 1.0 + threshold and
-- favorite NO price <= 0.30, buy favorite NO only (skip the losing hedge).
INSERT INTO strategy_config (strategy, enabled, updated_ts) VALUES
  ('cross-arb-favorite', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (strategy) DO NOTHING;
