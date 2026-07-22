-- Intent feature logging for offline learning (Addendum A.2.1, A.2.2).
-- Every intent — including gated ones — writes a row here so that
-- supervised datasets and off-policy evaluation have exact inputs.

CREATE TABLE IF NOT EXISTS intent_features (
  order_id     bigint PRIMARY KEY REFERENCES orders_v2(id),
  feature_hash text   NOT NULL,
  features     jsonb  NOT NULL,
  model_id     bigint,
  propensity   double precision
);

CREATE INDEX IF NOT EXISTS idx_intent_features_model ON intent_features (model_id);
CREATE INDEX IF NOT EXISTS idx_intent_features_hash ON intent_features (feature_hash);

-- Amend ticks_v2 with top-of-book depth columns (A.2.3).
-- Each PriceUpdate carries best bid/ask and displayed size, denormalized
-- so the realistic fill model (A.8) can join at the decision instant
-- without delta replay.
ALTER TABLE ticks_v2 ADD COLUMN IF NOT EXISTS best_bid_cents int;
ALTER TABLE ticks_v2 ADD COLUMN IF NOT EXISTS best_ask_cents int;
ALTER TABLE ticks_v2 ADD COLUMN IF NOT EXISTS best_bid_size int;
ALTER TABLE ticks_v2 ADD COLUMN IF NOT EXISTS best_ask_size int;
