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

// AppConfig holds technical/environment configuration loaded from YAML.
// These values are read once at startup and never change at runtime.
type AppConfig struct {
	Environment     string `yaml:"environment"`             // "demo" or "prod"
	APIKeyID        string `yaml:"kalshi_api_key_id"`       // Kalshi API key ID
	PrivateKeyPath  string `yaml:"kalshi_private_key_path"` // path to RSA PEM private key
	DBPath          string `yaml:"db_path"`                 // SQLite database path
	MetricsAddr     string `yaml:"metrics_addr"`            // metrics/pprof bind address (e.g. "127.0.0.1:6060")
	APITennisAPIKey string `yaml:"apitennis_api_key"`       // API-Tennis external API key
}

// Load reads the appropriate YAML config file based on APP_ENV.
// If APP_ENV is unset, prefers app.dev.yaml if it exists, else app.yaml.
func Load() (*AppConfig, error) {
	path, err := resolvePath()
	if err != nil {
		return nil, err
	}
	return LoadFromPath(path)
}

// LoadFromPath reads a specific YAML config file.
func LoadFromPath(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := cfg.validate(path); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate checks required fields and applies defaults.
func (c *AppConfig) validate(path string) error {
	if c.Environment == "" {
		return fmt.Errorf("environment is required in %s (demo or prod)", path)
	}
	if c.APIKeyID == "" {
		return fmt.Errorf("kalshi_api_key_id is required in %s", path)
	}
	if c.PrivateKeyPath == "" {
		return fmt.Errorf("kalshi_private_key_path is required in %s", path)
	}
	if c.DBPath == "" {
		c.DBPath = "kalshi_tennis.db"
	}
	if c.MetricsAddr == "" {
		c.MetricsAddr = "127.0.0.1:6060"
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
