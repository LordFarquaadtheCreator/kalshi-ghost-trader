-- Seed buythedip strategy config. Disabled by default for real trading.
-- Paper/simulated trades always run (see AGENTS.md global rule).
INSERT INTO strategy_config (strategy, enabled, updated_ts) VALUES
  ('buythedip', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (strategy) DO NOTHING;
