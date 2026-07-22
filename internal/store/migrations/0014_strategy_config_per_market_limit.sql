-- Add per_market_max_orders column to strategy_config.
-- 1 = default (one real buy per market per strategy). 0 = no limit.
-- Gates KalshiOrderEmitter buy path only — sells bypass to allow exits.
ALTER TABLE strategy_config
  ADD COLUMN IF NOT EXISTS per_market_max_orders INTEGER NOT NULL DEFAULT 1;
