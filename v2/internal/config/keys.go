package config

// keyClass describes how a config key is consumed.
type keyClass struct {
	topic string // "" = readLive; non-empty = subscribed(topic)
}

// Subscription topics. Components Subscribe to these.
const (
	topicSeries   = "series"
	topicRatelimit = "ratelimit"
	topicBackoff  = "backoff"
	topicGates    = "gates"
	topicRetention = "retention"
	topicInsights = "insights"
)

// keyClassifications is the single source of truth for how every app_config
// key is consumed. Every key the validator/buildSnapshot knows must appear
// here exactly once. TestEveryKeyClassified asserts this.
//
// readLive: read from Current() at use time — always fresh, no rebuild.
// subscribed(topic): a component rebuilds itself when the topic fires.
//
// The third state — silently stale at startup, never refreshed — is
// structurally impossible because any key not listed here is rejected by
// Update and flagged by TestEveryKeyClassified.
var keyClassifications = map[string]keyClass{
	// series_tickers — scanner rebuilds its series set on change.
	"series_tickers": {topic: topicSeries},

	// rate_limit_rps — rate limiter re-arms on change.
	"rate_limit_rps": {topic: topicRatelimit},
	"kalshi_livedata_rate_limit_rps": {topic: topicRatelimit},

	// ws_min_backoff_secs / ws_max_backoff_secs — WS manager updates backoff bounds.
	"ws_min_backoff_secs": {topic: topicBackoff},
	"ws_max_backoff_secs": {topic: topicBackoff},

	// strategy_config + trigger_ranges are loaded by the gate cache and
	// invalidated via the "gates" topic. The app_config keys that affect
	// gating behavior are grouped here.
	"per_strategy_cooldown_secs": {topic: topicGates},

	// retention_weeks — partition maintenance job reads on change.
	"retention_weeks": {topic: topicRetention},

	// insights_refresh_secs — insights refresh job re-arms on change.
	"insights_refresh_secs": {topic: topicInsights},

	// --- readLive keys (read from Current() at use time) ---
	"scan_interval_hours":               {},
	"track_lead_minutes":                {},
	"batch_size":                        {},
	"flush_timeout_ms":                  {},
	"http_timeout_secs":                 {},
	"scheduler_poll_secs":               {},
	"apitennis_timezone":                {},
	"kalshi_livedata_enabled":           {},
	"kalshi_livedata_poll_secs":         {},
	"close_timer_enabled":               {},
	"close_timer_lead_min":              {},
	"close_timer_min_price":             {},
	"close_timer_poll_secs":             {},
	"close_timer_size":                  {},
	"reconciler_interval_secs":          {},
	"order_backfill_interval_secs":      {},
	"schedule_checker_interval_secs":    {},
	"order_quota_enabled":               {},
	"order_quota_cooldown_secs":         {},
	"order_quota_max_per_sec":           {},
	"order_quota_budget_total":          {},
	"order_quota_budget_floor":          {},
	"real_trading_enabled":              {},
	"kelly_fraction":                    {},
	"paper_bankroll":                    {},
	"real_bankroll":                     {},
	"real_order_time_in_force":          {},
	"real_order_timeout_s":              {},
	"legacy_sizing":                     {},
}

// keyClassification returns the class for a key, plus whether it exists.
func keyClassification(key string) (keyClass, bool) {
	c, ok := keyClassifications[key]
	return c, ok
}

// allKnownKeys returns the set of keys the snapshot builder reads. This is
// the ground truth that TestEveryKeyClassified compares against
// keyClassifications.
func allKnownKeys() []string {
	return []string{
		"series_tickers",
		"scan_interval_hours",
		"track_lead_minutes",
		"ws_min_backoff_secs",
		"ws_max_backoff_secs",
		"batch_size",
		"flush_timeout_ms",
		"http_timeout_secs",
		"rate_limit_rps",
		"scheduler_poll_secs",
		"apitennis_timezone",
		"kalshi_livedata_enabled",
		"kalshi_livedata_poll_secs",
		"kalshi_livedata_rate_limit_rps",
		"close_timer_enabled",
		"close_timer_lead_min",
		"close_timer_min_price",
		"close_timer_poll_secs",
		"close_timer_size",
		"reconciler_interval_secs",
		"order_backfill_interval_secs",
		"schedule_checker_interval_secs",
		"order_quota_enabled",
		"order_quota_cooldown_secs",
		"order_quota_max_per_sec",
		"order_quota_budget_total",
		"order_quota_budget_floor",
		"per_strategy_cooldown_secs",
		"real_trading_enabled",
		"kelly_fraction",
		"paper_bankroll",
		"real_bankroll",
		"real_order_time_in_force",
		"real_order_timeout_s",
		"legacy_sizing",
		"retention_weeks",
		"insights_refresh_secs",
	}
}
