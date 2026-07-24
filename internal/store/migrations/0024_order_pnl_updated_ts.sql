-- Track when unrealized PnL was last written to resolved_pnl_cents.
-- While position is open, pnltracker goroutine writes live mark-to-market
-- PnL into resolved_pnl_cents every 30s. At settlement, ResolveRealOrders
-- overwrites with final realized PnL. This timestamp distinguishes live
-- vs final values.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS pnl_updated_ts BIGINT NOT NULL DEFAULT 0;
