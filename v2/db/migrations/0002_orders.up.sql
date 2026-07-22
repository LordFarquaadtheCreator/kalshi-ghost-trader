CREATE TABLE IF NOT EXISTS orders_v2 (
  id bigserial PRIMARY KEY,
  client_order_id uuid NOT NULL DEFAULT gen_random_uuid(),
  ts_intent bigint NOT NULL,
  ts_submitted bigint,
  ts_acked bigint,

  event_ticker text NOT NULL,
  market_ticker text NOT NULL,
  strategy text NOT NULL,
  action text NOT NULL,
  contracts int NOT NULL,
  price_cents int NOT NULL,
  conv_prob_bps int NOT NULL,
  reason text,

  status text NOT NULL DEFAULT 'intent' CHECK (status IN
    ('intent','gated','accepted','held','submitted','filled','partial','canceled','failed','unverified','settled')),
  gate_reason text,

  is_paper boolean NOT NULL DEFAULT true,
  kalshi_order_id text,
  fill_count int,
  fill_price_cents int,

  created_ts bigint NOT NULL DEFAULT (extract(epoch from now()) * 1000)::bigint,
  updated_ts bigint NOT NULL DEFAULT (extract(epoch from now()) * 1000)::bigint
);

CREATE INDEX IF NOT EXISTS idx_orders_v2_status ON orders_v2 (status);
CREATE INDEX IF NOT EXISTS idx_orders_v2_client_order_id ON orders_v2 (client_order_id);
CREATE INDEX IF NOT EXISTS idx_orders_v2_strategy ON orders_v2 (strategy);
CREATE INDEX IF NOT EXISTS idx_orders_v2_market ON orders_v2 (market_ticker);
