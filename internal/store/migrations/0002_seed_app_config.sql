-- Seed default app_config values. ON CONFLICT DO NOTHING so existing
-- keys are never overwritten — only missing keys get inserted.
-- This makes the table safe to ship empty; migration fills defaults
-- and LoadFromDB no longer requires manual seeding.

INSERT INTO app_config (key, value, updated_ts) VALUES
  ('series_tickers', '["KXATPMATCH","KXWTAMATCH","KXITFMATCH","KXITFWMATCH","KXATPCHALLENGERMATCH","KXWTACHALLENGERMATCH","KXTENNISEXHIBITION","KXCHALLENGERMATCH","KXATPDOUBLES","KXWTADOUBLES","KXITFDOUBLES","KXITFWDOUBLES"]', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('scan_interval_hours', '24', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('track_lead_minutes', '5', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('ws_min_backoff_secs', '1', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('ws_max_backoff_secs', '30', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('batch_size', '500', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('flush_timeout_ms', '250', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('http_timeout_secs', '30', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('rate_limit_rps', '15', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('scheduler_poll_secs', '30', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('apitennis_enabled', 'false', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('apitennis_timezone', '+00:00', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('kalshi_livedata_enabled', 'false', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('kalshi_livedata_poll_secs', '10', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('close_timer_enabled', 'false', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('close_timer_lead_min', '10', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('close_timer_min_price', '0.85', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('close_timer_poll_secs', '30', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('close_timer_size', '50', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('reconciler_interval_secs', '300', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('order_backfill_interval_secs', '120', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('schedule_checker_interval_secs', '120', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('order_quota_enabled', 'false', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('order_quota_cooldown_secs', '30', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('order_quota_max_per_sec', '5', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('order_quota_budget_total', '1000', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('order_quota_budget_floor', '5', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('per_strategy_cooldown_secs', '60', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('real_trading_enabled', 'false', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('kelly_fraction', '0.25', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('paper_bankroll', '1000', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('real_bankroll', '1000', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('real_order_time_in_force', 'immediate_or_cancel', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('real_order_timeout_s', '10', (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (key) DO NOTHING;

-- Seed liquidity pool with default 1000.00 if not yet initialized.
INSERT INTO liquidity_pool (id, balance_cents, initial_balance_cents, total_spent_cents, total_pnl_cents, updated_ts)
VALUES (1, 100000, 100000, 0, 0, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (id) DO NOTHING;

-- Seed strategy_config: all strategies disabled by default for real trading.
INSERT INTO strategy_config (strategy, enabled, updated_ts) VALUES
  ('matchpoint', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('matchpoint-aggro', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-serve', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setpoint-cheap', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('nofade', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('breakback', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('setdown', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('server1530', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('tiebreak', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('breakpoint', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('convexpool', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('comeback040', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('calibrated-markov', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('cross-arb', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('tiebreak-server', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('set1winner', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('volratio', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('surface-markov', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('spike-fade', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot-itf', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot-challenger', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot-atp', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot-wta', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot-doubles', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint),
  ('fadelongshot-evening', false, (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint)
ON CONFLICT (strategy) DO NOTHING;
