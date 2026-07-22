-- Separate rate limit for kalshi livedata pollers so they don't starve
-- scanner/reconciler. Default 5 rps — enough for ~50 concurrent matches at
-- 10s poll interval, leaves 15 rps for scanner/reconciler on shared client.
INSERT INTO app_config (key, value, updated_ts) VALUES
  ('kalshi_livedata_rate_limit_rps', '5', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (key) DO NOTHING;
