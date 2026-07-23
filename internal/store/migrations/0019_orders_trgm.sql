-- R.2: trigram index for fast match_ticker substring search on the
-- paper-orders route. pg_trgm must be installed by a superuser; if the
-- kalshi role lacks CREATE EXTENSION privilege, run this file manually
-- as the postgres superuser on mint.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_orders_match_trgm
  ON orders USING gin (match_ticker gin_trgm_ops);
