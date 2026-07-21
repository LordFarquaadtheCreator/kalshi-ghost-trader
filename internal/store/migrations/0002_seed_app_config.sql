-- Seed default app_config values. Uses INSERT OR IGNORE so existing
-- keys are never overwritten — only missing keys get inserted.
-- This makes the table safe to ship empty; migration fills defaults
-- and LoadFromDB no longer requires manual seeding.

INSERT OR IGNORE INTO app_config (key, value, updated_ts) VALUES
  ('series_tickers', '["KXATPMATCH","KXWTAMATCH","KXITFMATCH","KXITFWMATCH","KXATPCHALLENGERMATCH","KXWTACHALLENGERMATCH","KXTENNISEXHIBITION","KXCHALLENGERMATCH","KXATPDOUBLES","KXWTADOUBLES","KXITFDOUBLES","KXITFWDOUBLES"]', strftime('%s','now') * 1000),
  ('scan_interval_hours', '24', strftime('%s','now') * 1000),
  ('track_lead_minutes', '5', strftime('%s','now') * 1000),
  ('ws_min_backoff_secs', '1', strftime('%s','now') * 1000),
  ('ws_max_backoff_secs', '30', strftime('%s','now') * 1000),
  ('batch_size', '500', strftime('%s','now') * 1000),
  ('flush_timeout_ms', '250', strftime('%s','now') * 1000),
  ('http_timeout_secs', '30', strftime('%s','now') * 1000),
  ('rate_limit_rps', '15', strftime('%s','now') * 1000),
  ('scheduler_poll_secs', '30', strftime('%s','now') * 1000),
  ('apitennis_enabled', 'false', strftime('%s','now') * 1000),
  ('apitennis_timezone', '+00:00', strftime('%s','now') * 1000),
  ('kalshi_livedata_enabled', 'false', strftime('%s','now') * 1000),
  ('kalshi_livedata_poll_secs', '10', strftime('%s','now') * 1000),
  ('close_timer_enabled', 'false', strftime('%s','now') * 1000),
  ('close_timer_lead_min', '10', strftime('%s','now') * 1000),
  ('close_timer_min_price', '0.85', strftime('%s','now') * 1000),
  ('close_timer_poll_secs', '30', strftime('%s','now') * 1000),
  ('close_timer_size', '50', strftime('%s','now') * 1000),
  ('reconciler_interval_secs', '300', strftime('%s','now') * 1000),
  ('order_backfill_interval_secs', '120', strftime('%s','now') * 1000),
  ('schedule_checker_interval_secs', '120', strftime('%s','now') * 1000),
  ('order_quota_enabled', 'false', strftime('%s','now') * 1000),
  ('order_quota_cooldown_secs', '30', strftime('%s','now') * 1000),
  ('order_quota_max_per_sec', '5', strftime('%s','now') * 1000),
  ('order_quota_budget_total', '1000', strftime('%s','now') * 1000),
  ('order_quota_budget_floor', '5', strftime('%s','now') * 1000),
  ('per_strategy_cooldown_secs', '60', strftime('%s','now') * 1000),
  ('real_trading_enabled', 'false', strftime('%s','now') * 1000),
  ('kelly_fraction', '0.25', strftime('%s','now') * 1000),
  ('paper_bankroll', '1000', strftime('%s','now') * 1000),
  ('real_bankroll', '1000', strftime('%s','now') * 1000),
  ('real_order_time_in_force', 'immediate_or_cancel', strftime('%s','now') * 1000),
  ('real_order_timeout_s', '10', strftime('%s','now') * 1000);

-- Seed liquidity pool with default 1000.00 if not yet initialized.
INSERT OR IGNORE INTO liquidity_pool (id, balance_cents, initial_balance_cents, total_spent_cents, total_pnl_cents, updated_ts)
VALUES (1, 100000, 100000, 0, 0, strftime('%s','now') * 1000);

-- Seed strategy_config: all strategies disabled by default for real trading.
INSERT OR IGNORE INTO strategy_config (strategy, enabled, updated_ts) VALUES
  ('matchpoint', 0, strftime('%s','now') * 1000),
  ('matchpoint-aggro', 0, strftime('%s','now') * 1000),
  ('setpoint', 0, strftime('%s','now') * 1000),
  ('setpoint-serve', 0, strftime('%s','now') * 1000),
  ('setpoint-cheap', 0, strftime('%s','now') * 1000),
  ('fadelongshot', 0, strftime('%s','now') * 1000),
  ('nofade', 0, strftime('%s','now') * 1000),
  ('breakback', 0, strftime('%s','now') * 1000),
  ('setdown', 0, strftime('%s','now') * 1000),
  ('server1530', 0, strftime('%s','now') * 1000),
  ('tiebreak', 0, strftime('%s','now') * 1000),
  ('breakpoint', 0, strftime('%s','now') * 1000),
  ('convexpool', 0, strftime('%s','now') * 1000),
  ('comeback040', 0, strftime('%s','now') * 1000),
  ('calibrated-markov', 0, strftime('%s','now') * 1000),
  ('cross-arb', 0, strftime('%s','now') * 1000),
  ('tiebreak-server', 0, strftime('%s','now') * 1000),
  ('set1winner', 0, strftime('%s','now') * 1000),
  ('volratio', 0, strftime('%s','now') * 1000),
  ('surface-markov', 0, strftime('%s','now') * 1000),
  ('spike-fade', 0, strftime('%s','now') * 1000),
  ('fadelongshot-itf', 0, strftime('%s','now') * 1000),
  ('fadelongshot-challenger', 0, strftime('%s','now') * 1000),
  ('fadelongshot-atp', 0, strftime('%s','now') * 1000),
  ('fadelongshot-wta', 0, strftime('%s','now') * 1000),
  ('fadelongshot-doubles', 0, strftime('%s','now') * 1000),
  ('fadelongshot-evening', 0, strftime('%s','now') * 1000);
