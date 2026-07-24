-- Add fill_price column to orders. Stores actual Kalshi fill price per
-- contract (from taker_fill_cost_dollars / fill_count). NULL for paper
-- orders, zero-fills, and legacy rows. market_price remains the signal-time
-- price for traceability; fill_price is the truth for pool + P&L math.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS fill_price double precision;
