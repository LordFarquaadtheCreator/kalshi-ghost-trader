-- Enable live-detection in schedule checker by default.
-- Probes /milestones + /live_data to detect matches that started ahead of
-- their scheduled occurrence_datetime. Kalshi's occurrence_datetime is
-- unreliable for ITF matches — often a default future slot while the match
-- is already in progress. live_data.details.status is authoritative.
INSERT INTO app_config (key, value, updated_ts) VALUES
  ('schedule_checker_live_detection', 'true', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (key) DO NOTHING;
