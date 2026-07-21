-- 0007_sync_liquidity_pool_to_real_bankroll.sql
-- One-time bridge: if liquidity_pool is still at the old $1000 seed and
-- real_bankroll config has been changed, sync the pool to real_bankroll.
-- After this, dashboard reset/topup owns the pool — real_bankroll config
-- is no longer read for sizing (kelly reads pool balance live).
--
-- Idempotent: only fires when pool is exactly at the 100000-cent seed.
-- If pool has been touched (orders deducted, dashboard reset), this is a no-op.

UPDATE liquidity_pool
SET balance_cents = CAST(
        COALESCE((SELECT value FROM app_config WHERE key = 'real_bankroll'), '1000')
        AS INTEGER
    ) * 100,
    initial_balance_cents = CAST(
        COALESCE((SELECT value FROM app_config WHERE key = 'real_bankroll'), '1000')
        AS INTEGER
    ) * 100,
    updated_ts = (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint
WHERE id = 1
  AND balance_cents = 100000
  AND initial_balance_cents = 100000
  AND total_spent_cents = 0
  AND total_pnl_cents = 0;
