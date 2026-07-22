CREATE TABLE IF NOT EXISTS pool_ledger (
  id bigserial PRIMARY KEY,
  ts bigint NOT NULL,
  entry_type text NOT NULL CHECK (entry_type IN
    ('deposit','withdrawal','order_hold','hold_release','fill_cost','settlement_payout')),
  amount_cents bigint NOT NULL,
  order_id bigint,
  note text
);
CREATE INDEX IF NOT EXISTS idx_pool_ledger_order_id ON pool_ledger (order_id);
CREATE INDEX IF NOT EXISTS idx_pool_ledger_ts ON pool_ledger (ts);

CREATE TABLE IF NOT EXISTS pool_balance (
  id int PRIMARY KEY CHECK (id = 1),
  balance_cents bigint NOT NULL CHECK (balance_cents >= 0),
  updated_ts bigint NOT NULL
);
