package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config holds all runtime configuration loaded from env vars.
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
	BatchSize    int
	FlushTimeoutMS int

	// REST client timeout (seconds)
	HTTPTimeoutSecs int

	// Scheduler poll interval (seconds)
	SchedulerPollSecs int
}

// Load reads config from environment variables with sensible defaults.
// Logs warnings for malformed integer env vars instead of silently falling back.
func Load() (*Config, error) {
	log := slog.Default()

	cfg := &Config{
		APIKeyID:          os.Getenv("KALSHI_API_KEY_ID"),
		PrivateKeyPath:    os.Getenv("KALSHI_PRIVATE_KEY_PATH"),
		Environment:       envOr("KALSHI_ENV", "demo"),
		DBPath:            envOr("DB_PATH", "kalshi_tennis.db"),
		ScanIntervalHours: envIntOr("SCAN_INTERVAL_HOURS", 24, log),
		TrackLeadMinutes:  envIntOr("TRACK_LEAD_MINUTES", 5, log),
		WSMinBackoffSecs:  envIntOr("WS_MIN_BACKOFF_SECS", 1, log),
		WSMaxBackoffSecs:  envIntOr("WS_MAX_BACKOFF_SECS", 30, log),
		BatchSize:         envIntOr("BATCH_SIZE", 500, log),
		FlushTimeoutMS:    envIntOr("FLUSH_TIMEOUT_MS", 250, log),
		HTTPTimeoutSecs:   envIntOr("HTTP_TIMEOUT_SECS", 30, log),
		SchedulerPollSecs: envIntOr("SCHEDULER_POLL_SECS", 30, log),
	}

	// Core series: 8 tennis match-winner series
	cfg.SeriesTickers = strings.Split(envOr("SERIES_TICKERS",
		"KXATPMATCH,KXWTAMATCH,KXITFMATCH,KXITFWMATCH,KXATPCHALLENGERMATCH,KXWTACHALLENGERMATCH,KXTENNISEXHIBITION,KXCHALLENGERMATCH"),
		",")

	switch cfg.Environment {
	case "demo":
		cfg.RESTBaseURL = "https://external-api.demo.kalshi.co/trade-api/v2"
		cfg.WSURL = "wss://external-api-ws.demo.kalshi.co/trade-api/ws/v2"
	case "prod":
		cfg.RESTBaseURL = "https://external-api.kalshi.com/trade-api/v2"
		cfg.WSURL = "wss://external-api-ws.kalshi.com/trade-api/ws/v2"
	default:
		return nil, fmt.Errorf("KALSHI_ENV must be 'demo' or 'prod', got: %s", cfg.Environment)
	}

	if cfg.APIKeyID == "" {
		return nil, fmt.Errorf("KALSHI_API_KEY_ID is required")
	}
	if cfg.PrivateKeyPath == "" {
		return nil, fmt.Errorf("KALSHI_PRIVATE_KEY_PATH is required")
	}

	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int, log *slog.Logger) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		log.Warn("invalid integer env var, using default", "key", key, "value", v, "default", def)
		return def
	}
	return n
}
