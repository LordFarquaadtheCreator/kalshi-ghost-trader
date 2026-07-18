// Package config loads runtime configuration from the SQLite database.
//
// The Config struct holds all tunable parameters for the ghost-trader service:
// Kalshi API credentials, environment selection (demo/prod), SQLite path,
// tennis series tickers, scanner/scheduler intervals, WebSocket backoff,
// batch sizes, rate limits, metrics port, and API-Tennis scraper settings.
//
// Configuration is loaded via [LoadFromDB], which reads all keys from the
// `app_config` table, applies defaults for unset fields, and derives
// REST/WebSocket URLs from the environment.
//
// The migration tool (cmd/migrate-config) reads config.yaml once and seeds
// the app_config table. The main app never reads config.yaml.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Config holds all runtime configuration loaded from the app_config DB table.
type Config struct {
	// Kalshi API credentials
	APIKeyID       string
	PrivateKeyPath string

	// Environment: "demo" or "prod"
	Environment string

	// REST base URL (derived from Environment)
	RESTBaseURL string
	// WebSocket URL (derived from Environment)
	WSURL string

	// SQLite database path
	DBPath string

	// Tennis series to scan
	SeriesTickers []string

	// Scanner interval for daily scan (hours)
	ScanIntervalHours int

	// How early before occurrence_datetime to start tracking (minutes)
	TrackLeadMinutes int

	// WebSocket reconnection backoff
	WSMinBackoffSecs int
	WSMaxBackoffSecs int

	// SQLite batch settings
	BatchSize      int
	FlushTimeoutMS int

	// REST client timeout (seconds)
	HTTPTimeoutSecs int

	// REST client max requests per second (0 = use client default)
	RateLimitRPS int

	// Scheduler poll interval (seconds)
	SchedulerPollSecs int

	// Reconciler poll interval (seconds) — fills settlement gaps via REST
	ReconcilerIntervalSecs int

	// Schedule checker poll interval (seconds) — refreshes stale occurrence_ts from REST
	ScheduleCheckerIntervalSecs int

	// pprof/metrics HTTP server port (0 = disabled)
	MetricsPort int

	// API-Tennis scraper settings (WebSocket real-time push)
	APITennisEnabled  bool
	APITennisAPIKey   string
	APITennisTimezone string

	// Close timer strategy: buy the favorite N minutes before market close.
	CloseTimerEnabled  bool
	CloseTimerLeadMin  int
	CloseTimerMinPrice float64
	CloseTimerPollSecs int
	CloseTimerSize     float64

	// Order quota — throttles order emission to prevent exhausting API quota.
	OrderQuotaEnabled      bool
	OrderQuotaCooldownSecs int
	OrderQuotaMaxPerSec    int
	OrderQuotaDailyLimit   int
	OrderQuotaBudgetTotal  float64
	OrderQuotaBudgetFloor  float64

	// Per-strategy cooldown (seconds) — prevents same strategy firing too fast across markets
	PerStrategyCooldownSecs int

	// Real trading
	RealTradingEnabled bool
	KellyFraction      float64
	PaperBankroll      float64

	// Real order config
	RealBankroll         float64
	RealOrderTimeInForce string
	RealOrderTimeoutS    int
}

// LoadFromDB reads all configuration from the app_config table in the SQLite DB.
// Returns error if app_config is empty (migration not run yet).
func LoadFromDB(db *store.DB) (*Config, error) {
	ctx := context.Background()
	pairs, err := db.GetAllAppConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read app_config: %w", err)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("app_config table is empty — run migrate-config first")
	}

	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		m[kv.Key] = kv.Value
	}

	cfg := &Config{}
	cfg.applyFromMap(m)
	cfg.applyDefaults(slog.Default())
	cfg.applyNewDefaults()

	switch cfg.Environment {
	case "demo":
		cfg.RESTBaseURL = "https://external-api.demo.kalshi.co/trade-api/v2"
		cfg.WSURL = "wss://external-api-ws.demo.kalshi.co/trade-api/ws/v2"
	case "prod":
		cfg.RESTBaseURL = "https://external-api.kalshi.com/trade-api/v2"
		cfg.WSURL = "wss://external-api-ws.kalshi.com/trade-api/ws/v2"
	default:
		return nil, fmt.Errorf("environment must be 'demo' or 'prod', got: %s", cfg.Environment)
	}

	if cfg.APIKeyID == "" {
		return nil, fmt.Errorf("api_key_id is required")
	}
	if cfg.PrivateKeyPath == "" {
		return nil, fmt.Errorf("private_key_path is required")
	}

	if cfg.CloseTimerEnabled && cfg.CloseTimerLeadMin > cfg.TrackLeadMinutes {
		return nil, fmt.Errorf("close_timer_lead_minutes (%d) cannot exceed track_lead_minutes (%d) — WS not subscribed until T-track_lead",
			cfg.CloseTimerLeadMin, cfg.TrackLeadMinutes)
	}

	return cfg, nil
}

// ConfigCache provides thread-safe access to Config with refresh capability.
// Dashboard updates call Refresh() after writing to app_config.
type ConfigCache struct {
	mu  sync.RWMutex
	cfg *Config
	db  *store.DB
}

// NewConfigCache creates a cache wrapping the current config.
func NewConfigCache(db *store.DB, cfg *Config) *ConfigCache {
	return &ConfigCache{cfg: cfg, db: db}
}

// Get returns the current config (thread-safe snapshot).
func (c *ConfigCache) Get() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// Refresh reloads config from the DB and swaps the cached copy.
func (c *ConfigCache) Refresh() error {
	cfg, err := LoadFromDB(c.db)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	return nil
}

// Update writes a single key to app_config and refreshes the cache.
func (c *ConfigCache) Update(key, value string) error {
	ctx := context.Background()
	if err := c.db.SetAppConfig(ctx, key, value); err != nil {
		return err
	}
	return c.Refresh()
}

// UpdateBatch writes multiple keys and refreshes the cache.
func (c *ConfigCache) UpdateBatch(pairs []store.AppConfigKV) error {
	ctx := context.Background()
	if err := c.db.SetAppConfigBatch(ctx, pairs); err != nil {
		return err
	}
	return c.Refresh()
}

// applyFromMap populates Config fields from a key-value map.
func (c *Config) applyFromMap(m map[string]string) {
	c.APIKeyID = m["api_key_id"]
	c.PrivateKeyPath = m["private_key_path"]
	c.Environment = m["environment"]
	c.DBPath = getEnv("DB_PATH", "kalshi_tennis.db")

	c.SeriesTickers = parseJSONStringArray(m["series_tickers"])

	c.ScanIntervalHours = atoi(m["scan_interval_hours"])
	c.TrackLeadMinutes = atoi(m["track_lead_minutes"])
	c.WSMinBackoffSecs = atoi(m["ws_min_backoff_secs"])
	c.WSMaxBackoffSecs = atoi(m["ws_max_backoff_secs"])
	c.BatchSize = atoi(m["batch_size"])
	c.FlushTimeoutMS = atoi(m["flush_timeout_ms"])
	c.HTTPTimeoutSecs = atoi(m["http_timeout_secs"])
	c.RateLimitRPS = atoi(m["rate_limit_rps"])
	c.SchedulerPollSecs = atoi(m["scheduler_poll_secs"])
	c.MetricsPort = atoi(m["metrics_port"])

	c.APITennisEnabled = atob(m["apitennis_enabled"])
	c.APITennisAPIKey = m["apitennis_api_key"]
	c.APITennisTimezone = m["apitennis_timezone"]

	c.CloseTimerEnabled = atob(m["close_timer_enabled"])
	c.CloseTimerLeadMin = atoi(m["close_timer_lead_min"])
	c.CloseTimerMinPrice = atof(m["close_timer_min_price"])
	c.CloseTimerPollSecs = atoi(m["close_timer_poll_secs"])
	c.CloseTimerSize = atof(m["close_timer_size"])

	c.ReconcilerIntervalSecs = atoi(m["reconciler_interval_secs"])
	c.ScheduleCheckerIntervalSecs = atoi(m["schedule_checker_interval_secs"])

	c.OrderQuotaEnabled = atob(m["order_quota_enabled"])
	c.OrderQuotaCooldownSecs = atoi(m["order_quota_cooldown_secs"])
	c.OrderQuotaMaxPerSec = atoi(m["order_quota_max_per_sec"])
	c.OrderQuotaDailyLimit = atoi(m["order_quota_daily_limit"])
	c.OrderQuotaBudgetTotal = atof(m["order_quota_budget_total"])
	c.OrderQuotaBudgetFloor = atof(m["order_quota_budget_floor"])

	c.PerStrategyCooldownSecs = atoi(m["per_strategy_cooldown_secs"])

	c.RealTradingEnabled = atob(m["real_trading_enabled"])
	c.KellyFraction = atof(m["kelly_fraction"])
	c.PaperBankroll = atof(m["paper_bankroll"])

	c.RealBankroll = atof(m["real_bankroll"])
	c.RealOrderTimeInForce = m["real_order_time_in_force"]
	c.RealOrderTimeoutS = atoi(m["real_order_timeout_s"])
}

// applyDefaults fills zero-valued fields with sensible defaults.
func (c *Config) applyDefaults(log *slog.Logger) {
	if c.Environment == "" {
		c.Environment = "demo"
	}
	if c.DBPath == "" {
		c.DBPath = "kalshi_tennis.db"
	}
	if len(c.SeriesTickers) == 0 {
		c.SeriesTickers = []string{
			"KXATPMATCH", "KXWTAMATCH",
			"KXITFMATCH", "KXITFWMATCH",
			"KXATPCHALLENGERMATCH", "KXWTACHALLENGERMATCH",
			"KXTENNISEXHIBITION", "KXCHALLENGERMATCH",
			"KXATPDOUBLES", "KXWTADOUBLES",
			"KXITFDOUBLES", "KXITFWDOUBLES",
		}
	}
	if c.ScanIntervalHours == 0 {
		c.ScanIntervalHours = 24
	}
	if c.TrackLeadMinutes == 0 {
		c.TrackLeadMinutes = 5
	}
	if c.WSMinBackoffSecs == 0 {
		c.WSMinBackoffSecs = 1
	}
	if c.WSMaxBackoffSecs == 0 {
		c.WSMaxBackoffSecs = 30
	}
	if c.BatchSize == 0 {
		c.BatchSize = 500
	}
	if c.FlushTimeoutMS == 0 {
		c.FlushTimeoutMS = 250
	}
	if c.HTTPTimeoutSecs == 0 {
		c.HTTPTimeoutSecs = 30
	}
	if c.RateLimitRPS == 0 {
		c.RateLimitRPS = 15
	}
	if c.SchedulerPollSecs == 0 {
		c.SchedulerPollSecs = 30
	}
	if c.MetricsPort == 0 {
		c.MetricsPort = 6060
	}
	if c.APITennisTimezone == "" {
		c.APITennisTimezone = "+00:00"
	}
	if c.CloseTimerLeadMin == 0 {
		c.CloseTimerLeadMin = 10
	}
	if c.CloseTimerMinPrice == 0 {
		c.CloseTimerMinPrice = 0.85
	}
	if c.CloseTimerPollSecs == 0 {
		c.CloseTimerPollSecs = 30
	}
	if c.CloseTimerSize == 0 {
		c.CloseTimerSize = 50
	}
	if c.ReconcilerIntervalSecs == 0 {
		c.ReconcilerIntervalSecs = 300
	}
	if c.ScheduleCheckerIntervalSecs == 0 {
		c.ScheduleCheckerIntervalSecs = 120
	}
	if c.OrderQuotaCooldownSecs == 0 {
		c.OrderQuotaCooldownSecs = 30
	}
	if c.OrderQuotaMaxPerSec == 0 {
		c.OrderQuotaMaxPerSec = 50
	}
	if c.OrderQuotaDailyLimit == 0 {
		c.OrderQuotaDailyLimit = 1000
	}
	if c.OrderQuotaBudgetFloor == 0 && c.OrderQuotaBudgetTotal > 0 {
		c.OrderQuotaBudgetFloor = 5.0
	}
}

// addDefaults for new fields not in the old applyDefaults
func (c *Config) applyNewDefaults() {
	if c.PerStrategyCooldownSecs == 0 {
		c.PerStrategyCooldownSecs = 60
	}
	if c.KellyFraction == 0 {
		c.KellyFraction = 0.25
	}
	if c.PaperBankroll == 0 {
		c.PaperBankroll = 1000
	}
	if c.RealBankroll == 0 {
		c.RealBankroll = 1000
	}
	if c.RealOrderTimeInForce == "" {
		c.RealOrderTimeInForce = "immediate_or_cancel"
	}
	if c.RealOrderTimeoutS == 0 {
		c.RealOrderTimeoutS = 10
	}
}

// --- helpers ---

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

func atof(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func atob(s string) bool {
	return s == "true" || s == "1"
}

func parseJSONStringArray(s string) []string {
	if s == "" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		// fallback: comma-separated
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				arr = append(arr, p)
			}
		}
	}
	return arr
}
