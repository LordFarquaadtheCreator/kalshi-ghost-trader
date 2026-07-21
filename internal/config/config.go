// Package config provides a globally accessible Config that combines
// environment config (from app.yaml, read-only) and runtime config
// (from app_config DB table, dashboard-editable via RuntimeConfig CRUD).
//
// Usage:
//
//	cfg := config.Load(db)        // call once at startup
//	cfg.Runtime.RealTradingEnabled
//	cfg.Env.RESTBaseURL
//	cfg.Runtime.Update(key, val)  // dashboard writes
package config

import (
	"github.com/farquaad/kalshi-ghost-trader/internal/appconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/runtimeconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Config combines environment and runtime configuration.
// EnvConfig is embedded (immutable), RuntimeConfig is embedded (mutable via Update/UpdateBatch/Delete).
type Config struct {
	*appconfig.EnvConfig
	*runtimeconfig.RuntimeConfig
}

// Cfg is the global config set by Load. Access directly after Load returns.
var Cfg *Config

// Load initializes env config from app.yaml and runtime config from DB,
// sets Cfg, and returns the combined Config.
func Load(db *store.DB) (*Config, error) {
	env, err := appconfig.Load()
	if err != nil {
		return nil, err
	}
	rc, err := runtimeconfig.LoadFromDB(db)
	if err != nil {
		return nil, err
	}
	Cfg = &Config{EnvConfig: env, RuntimeConfig: rc}
	return Cfg, nil
}
