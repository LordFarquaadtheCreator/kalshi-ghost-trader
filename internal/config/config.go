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
	BatchSize     int `yaml:"batch_size"`
	FlushTimeoutMS int `yaml:"flush_timeout_ms"`

	// REST client timeout (seconds)
	HTTPTimeoutSecs int `yaml:"http_timeout_secs"`

	// REST client max requests per second (0 = use client default)
	RateLimitRPS int `yaml:"rate_limit_rps"`

	// Scheduler poll interval (seconds)
	SchedulerPollSecs int `yaml:"scheduler_poll_secs"`

	// pprof/metrics HTTP server port (0 = disabled)
	MetricsPort int `yaml:"metrics_port"`
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
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
