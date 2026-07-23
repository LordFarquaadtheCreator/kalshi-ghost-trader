-- R.7: control raw JSON payload retention.
-- store_raw_payloads: when false, WS handlers skip payload column on ingest.
-- payload_retention_hours: janitor nulls payloads older than this (0=disabled).
INSERT INTO app_config (key, value, updated_ts) VALUES
  ('store_raw_payloads', 'true', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('payload_retention_hours', '72', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (key) DO NOTHING;
