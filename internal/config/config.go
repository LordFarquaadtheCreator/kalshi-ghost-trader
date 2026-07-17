// Package config loads runtime configuration from a YAML file (config.yaml).
//
// The Config struct holds all tunable parameters for the ghost-trader service:
// Kalshi API credentials, environment selection (demo/prod), SQLite path,
// tennis series tickers, scanner/scheduler intervals, WebSocket backoff,
// batch sizes, rate limits, metrics port, and FlashScore scraper settings.
//
// Configuration is loaded via [Load], which reads config.yaml (or the path
// specified by the CONFIG_PATH environment variable), applies defaults for
// unset fields, and derives REST/WebSocket URLs from the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration loaded from config.yaml.
type Config struct {
	// Kalshi API credentials
	APIKeyID       string `yaml:"api_key_id"`
	PrivateKeyPath string `yaml:"private_key_path"`

	// Environment: "demo" or "prod"
	Environment string `yaml:"environment"`

	// REST base URL (derived from Environment)
	RESTBaseURL string `yaml:"-"`
	// WebSocket URL (derived from Environment)
	WSURL string `yaml:"-"`

	// SQLite database path
	DBPath string `yaml:"db_path"`

	// Tennis series to scan
	SeriesTickers []string `yaml:"series_tickers"`

	// Scanner interval for daily scan (hours)
	ScanIntervalHours int `yaml:"scan_interval_hours"`

	// How early before occurrence_datetime to start tracking (minutes)
	TrackLeadMinutes int `yaml:"track_lead_minutes"`

	// WebSocket reconnection backoff
	WSMinBackoffSecs int `yaml:"ws_min_backoff_secs"`
	WSMaxBackoffSecs int `yaml:"ws_max_backoff_secs"`

	// SQLite batch settings
	BatchSize      int `yaml:"batch_size"`
	FlushTimeoutMS int `yaml:"flush_timeout_ms"`

	// REST client timeout (seconds)
	HTTPTimeoutSecs int `yaml:"http_timeout_secs"`

	// REST client max requests per second (0 = use client default)
	RateLimitRPS int `yaml:"rate_limit_rps"`

	// Scheduler poll interval (seconds)
	SchedulerPollSecs int `yaml:"scheduler_poll_secs"`

	// Reconciler poll interval (seconds) — fills settlement gaps via REST
	ReconcilerIntervalSecs int `yaml:"reconciler_interval_secs"`

	// pprof/metrics HTTP server port (0 = disabled)
	MetricsPort int `yaml:"metrics_port"`

	// FlashScore scraper settings
	FlashScoreEnabled       bool `yaml:"flashscore_enabled"`
	FlashScoreScanInterval  int  `yaml:"flashscore_scan_interval_secs"` // feed scan interval
	FlashScorePollInterval  int  `yaml:"flashscore_poll_interval_secs"` // point poll interval
	FlashScoreLookaheadDays int  `yaml:"flashscore_lookahead_days"`     // days to look ahead in feed

	// API-Tennis scraper settings (WebSocket real-time push)
	APITennisEnabled  bool   `yaml:"apitennis_enabled"`
	APITennisAPIKey   string `yaml:"apitennis_api_key"`
	APITennisTimezone string `yaml:"apitennis_timezone"` // e.g. "+00:00", "-05:00"

	// Close timer strategy: buy the favorite N minutes before market close.
	// Empirical edge: favorite priced ≥85c at T-10min won 100% in backtest.
	CloseTimerEnabled  bool    `yaml:"close_timer_enabled"`
	CloseTimerLeadMin  int     `yaml:"close_timer_lead_minutes"` // fire this many min before close
	CloseTimerMinPrice float64 `yaml:"close_timer_min_price"`    // only buy favorites ≥ this price
	CloseTimerPollSecs int     `yaml:"close_timer_poll_secs"`    // DB poll interval
	CloseTimerSize     float64 `yaml:"close_timer_size"`         // shares per order
}

// Load reads config from config.yaml in the working directory.
// Path can be overridden via CONFIG_PATH env var.
func Load() (*Config, error) {
	log := slog.Default()

	path := envOr("CONFIG_PATH", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	// Apply defaults for unset fields
	cfg.applyDefaults(log)

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
	if c.FlashScoreScanInterval == 0 {
		c.FlashScoreScanInterval = 300 // 5 min
	}
	if c.FlashScorePollInterval == 0 {
		c.FlashScorePollInterval = 10 // 10 sec
	}
	if c.FlashScoreLookaheadDays == 0 {
		c.FlashScoreLookaheadDays = 1
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
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
