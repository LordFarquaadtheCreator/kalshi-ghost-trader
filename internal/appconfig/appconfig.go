// Package appconfig loads technical/environment configuration from YAML files.
//
// Two-layer config system:
//   - app.yaml / app.dev.yaml — technical config (environment, credentials, paths)
//   - app_config DB table — runtime tunables (intervals, strategy params, bankroll)
//
// File selection:
//   - APP_ENV=dev  → app.dev.yaml
//   - APP_ENV=prod → app.yaml
//   - unset        → app.dev.yaml if it exists, else app.yaml
//
// Dev machines keep app.dev.yaml and auto-run dev. Prod boxes only have app.yaml.
package appconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// EnvConfig holds technical/environment configuration loaded from YAML.
// These values are read once at startup and never change at runtime.
type EnvConfig struct {
	Environment         string `yaml:"environment"`             // "demo" or "prod"
	APIKeyID            string `yaml:"kalshi_api_key_id"`       // Kalshi API key ID
	PrivateKeyPath      string `yaml:"kalshi_private_key_path"` // path to RSA PEM private key
	DBDSN               string `yaml:"db_dsn"`                  // PostgreSQL DSN
	MetricsAddr         string `yaml:"metrics_addr"`            // metrics/pprof bind address (e.g. "127.0.0.1:6060")
	APITennisAPIKey     string `yaml:"apitennis_api_key"`       // API-Tennis external API key
	DisableWSDataSave   bool   `yaml:"disable_ws_data_save"`    // skip persisting Kalshi WS data to DB
	RESTBaseURL         string `yaml:"rest_base_url"`           // Kalshi REST API base URL
	WSURL               string `yaml:"ws_url"`                  // Kalshi WebSocket URL
	BacktestCacheTTLMin int    `yaml:"backtest_cache_ttl_min"`  // deprecated — backtest now persisted to DB
}

// Load reads the appropriate YAML config file based on APP_ENV.
// If APP_ENV is unset, prefers app.dev.yaml if it exists, else app.yaml.
func Load() (*EnvConfig, error) {
	path, err := resolvePath()
	if err != nil {
		return nil, err
	}
	return LoadFromPath(path)
}

// LoadFromPath reads a specific YAML config file.
func LoadFromPath(path string) (*EnvConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg EnvConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := cfg.validate(path); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate checks required fields and applies defaults.
func (c *EnvConfig) validate(path string) error {
	if c.Environment == "" {
		return fmt.Errorf("environment is required in %s (demo or prod)", path)
	}
	if c.RESTBaseURL == "" {
		return fmt.Errorf("rest_base_url is required in %s", path)
	}
	if c.WSURL == "" {
		return fmt.Errorf("ws_url is required in %s", path)
	}
	if c.DBDSN == "" {
		return fmt.Errorf("db_dsn is required in %s", path)
	}
	if c.MetricsAddr == "" {
		return fmt.Errorf("metrics_addr is required in %s", path)
	}
	return nil
}

func resolvePath() (string, error) {
	switch os.Getenv("APP_ENV") {
	case "dev":
		if _, err := os.Stat("app.dev.yaml"); err != nil {
			return "", fmt.Errorf("APP_ENV=dev but app.dev.yaml not found: %w", err)
		}
		return "app.dev.yaml", nil
	case "prod":
		return "app.yaml", nil
	default:
		if _, err := os.Stat("app.dev.yaml"); err == nil {
			fmt.Fprintln(os.Stderr, "warning: APP_ENV unset, defaulting to app.dev.yaml")
			return "app.dev.yaml", nil
		}
		return "app.yaml", nil
	}
}
