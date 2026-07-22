-- Backfill: AutoMigrate added per_market_max_orders with 0 before migration 0014
-- could apply DEFAULT 1. Set existing rows to the intended default of 1.
-- Future rows get DEFAULT 1 from the column definition.
UPDATE strategy_config SET per_market_max_orders = 1 WHERE per_market_max_orders = 0;
