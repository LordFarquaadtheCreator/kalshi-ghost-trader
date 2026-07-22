ALTER TABLE ticks_v2 DROP COLUMN IF EXISTS best_bid_cents;
ALTER TABLE ticks_v2 DROP COLUMN IF EXISTS best_ask_cents;
ALTER TABLE ticks_v2 DROP COLUMN IF EXISTS best_bid_size;
ALTER TABLE ticks_v2 DROP COLUMN IF EXISTS best_ask_size;

DROP TABLE IF EXISTS intent_features;
