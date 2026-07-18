// Command migrate-config is a one-time tool that reads config.yaml and seeds
// the app_config, liquidity_pool, and strategy_config tables in the SQLite DB.
// Run once, then delete config.yaml.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	APIKeyID       string   `yaml:"api_key_id"`
	PrivateKeyPath string   `yaml:"private_key_path"`
	Environment    string   `yaml:"environment"`
	DBPath         string   `yaml:"db_path"`
	SeriesTickers  []string `yaml:"series_tickers"`

	ScanIntervalHours int `yaml:"scan_interval_hours"`
	TrackLeadMinutes  int `yaml:"track_lead_minutes"`
	WSMinBackoffSecs  int `yaml:"ws_min_backoff_secs"`
	WSMaxBackoffSecs  int `yaml:"ws_max_backoff_secs"`
	BatchSize         int `yaml:"batch_size"`
	FlushTimeoutMS    int `yaml:"flush_timeout_ms"`
	HTTPTimeoutSecs   int `yaml:"http_timeout_secs"`
	RateLimitRPS      int `yaml:"rate_limit_rps"`
	SchedulerPollSecs int `yaml:"scheduler_poll_secs"`
	MetricsPort       int `yaml:"metrics_port"`

	APITennisEnabled  bool   `yaml:"apitennis_enabled"`
	APITennisAPIKey   string `yaml:"apitennis_api_key"`
	APITennisTimezone string `yaml:"apitennis_timezone"`

	CloseTimerEnabled  bool    `yaml:"close_timer_enabled"`
	CloseTimerLeadMin  int     `yaml:"close_timer_lead_minutes"`
	CloseTimerMinPrice float64 `yaml:"close_timer_min_price"`
	CloseTimerPollSecs int     `yaml:"close_timer_poll_secs"`
	CloseTimerSize     float64 `yaml:"close_timer_size"`

	ReconcilerIntervalSecs      int `yaml:"reconciler_interval_secs"`
	ScheduleCheckerIntervalSecs int `yaml:"schedule_checker_interval_secs"`

	OrderQuotaEnabled      bool    `yaml:"order_quota_enabled"`
	OrderQuotaCooldownSecs int     `yaml:"order_quota_cooldown_secs"`
	OrderQuotaMaxPerSec    int     `yaml:"order_quota_max_per_sec"`
	OrderQuotaBudgetTotal  float64 `yaml:"order_quota_budget_total"`
	OrderQuotaBudgetFloor  float64 `yaml:"order_quota_budget_floor"`
}

func main() {
	log := slog.Default()

	path := "config.yaml"
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		path = v
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Error("read config.yaml", "path", path, "err", err)
		os.Exit(1)
	}

	var yc yamlConfig
	if err := yaml.Unmarshal(data, &yc); err != nil {
		log.Error("parse config.yaml", "err", err)
		os.Exit(1)
	}

	dbPath := yc.DBPath
	if dbPath == "" {
		dbPath = "kalshi_tennis.db"
	}

	ctx := context.Background()
	db, err := store.New(ctx, dbPath, log)
	if err != nil {
		log.Error("open db", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Build app_config pairs
	pairs := buildConfigPairs(&yc)

	if err := db.SetAppConfigBatch(ctx, pairs); err != nil {
		log.Error("seed app_config", "err", err)
		os.Exit(1)
	}
	log.Info("seeded app_config", "keys", len(pairs))

	// Prune stale keys no longer in the config schema
	validKeys := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		validKeys[p.Key] = true
	}
	existing, err := db.GetAllAppConfig(ctx)
	if err != nil {
		log.Error("read app_config for prune", "err", err)
		os.Exit(1)
	}
	var stale []string
	for _, kv := range existing {
		if !validKeys[kv.Key] {
			stale = append(stale, kv.Key)
		}
	}
	for _, k := range stale {
		if err := db.DeleteAppConfig(ctx, k); err != nil {
			log.Error("prune stale key", "key", k, "err", err)
		}
	}
	if len(stale) > 0 {
		log.Info("pruned stale keys", "count", len(stale), "keys", stale)
	}

	// Seed liquidity pool from order_quota_budget_total
	initialCents := int64(yc.OrderQuotaBudgetTotal * 100)
	if initialCents > 0 {
		if err := db.InitLiquidityPool(ctx, initialCents); err != nil {
			log.Error("seed liquidity_pool", "err", err)
			os.Exit(1)
		}
		log.Info("seeded liquidity_pool", "initial_cents", initialCents)
	} else {
		log.Warn("order_quota_budget_total is 0 — liquidity_pool not seeded. Set initial balance via dashboard.")
	}

	// Seed strategy_config with all known strategies disabled
	strategies := []string{
		"matchpoint", "matchpoint-aggro", "setpoint", "setpoint-serve", "setpoint-cheap",
		"fadelongshot", "nofade", "breakback", "setdown", "server1530",
		"tiebreak-server", "spike-fade", "convexpool", "calibrated-markov",
		"surface-markov", "volratio", "comeback040", "set1winner", "tiebreak",
		"breakpoint", "closertimer",
	}
	for _, s := range strategies {
		if err := db.EnsureStrategyConfig(ctx, s); err != nil {
			log.Error("seed strategy_config", "strategy", s, "err", err)
			os.Exit(1)
		}
	}
	log.Info("seeded strategy_config", "strategies", len(strategies))

	log.Info("migration complete. You can now delete config.yaml.")
}

func buildConfigPairs(yc *yamlConfig) []store.AppConfigKV {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	_ = now

	seriesJSON, _ := json.Marshal(yc.SeriesTickers)

	pairs := []store.AppConfigKV{
		{Key: "api_key_id", Value: yc.APIKeyID},
		{Key: "private_key_path", Value: yc.PrivateKeyPath},
		{Key: "environment", Value: yc.Environment},
		{Key: "series_tickers", Value: string(seriesJSON)},
		{Key: "scan_interval_hours", Value: strconv.Itoa(yc.ScanIntervalHours)},
		{Key: "track_lead_minutes", Value: strconv.Itoa(yc.TrackLeadMinutes)},
		{Key: "ws_min_backoff_secs", Value: strconv.Itoa(yc.WSMinBackoffSecs)},
		{Key: "ws_max_backoff_secs", Value: strconv.Itoa(yc.WSMaxBackoffSecs)},
		{Key: "batch_size", Value: strconv.Itoa(yc.BatchSize)},
		{Key: "flush_timeout_ms", Value: strconv.Itoa(yc.FlushTimeoutMS)},
		{Key: "http_timeout_secs", Value: strconv.Itoa(yc.HTTPTimeoutSecs)},
		{Key: "rate_limit_rps", Value: strconv.Itoa(yc.RateLimitRPS)},
		{Key: "scheduler_poll_secs", Value: strconv.Itoa(yc.SchedulerPollSecs)},
		{Key: "metrics_port", Value: strconv.Itoa(yc.MetricsPort)},
		{Key: "apitennis_enabled", Value: boolStr(yc.APITennisEnabled)},
		{Key: "apitennis_api_key", Value: yc.APITennisAPIKey},
		{Key: "apitennis_timezone", Value: yc.APITennisTimezone},
		{Key: "close_timer_enabled", Value: boolStr(yc.CloseTimerEnabled)},
		{Key: "close_timer_lead_min", Value: strconv.Itoa(yc.CloseTimerLeadMin)},
		{Key: "close_timer_min_price", Value: fmt.Sprintf("%g", yc.CloseTimerMinPrice)},
		{Key: "close_timer_poll_secs", Value: strconv.Itoa(yc.CloseTimerPollSecs)},
		{Key: "close_timer_size", Value: fmt.Sprintf("%g", yc.CloseTimerSize)},
		{Key: "reconciler_interval_secs", Value: strconv.Itoa(yc.ReconcilerIntervalSecs)},
		{Key: "schedule_checker_interval_secs", Value: strconv.Itoa(yc.ScheduleCheckerIntervalSecs)},
		{Key: "order_quota_enabled", Value: boolStr(yc.OrderQuotaEnabled)},
		{Key: "order_quota_cooldown_secs", Value: strconv.Itoa(yc.OrderQuotaCooldownSecs)},
		{Key: "order_quota_max_per_sec", Value: strconv.Itoa(yc.OrderQuotaMaxPerSec)},
		{Key: "order_quota_budget_total", Value: fmt.Sprintf("%g", yc.OrderQuotaBudgetTotal)},
		{Key: "order_quota_budget_floor", Value: fmt.Sprintf("%g", yc.OrderQuotaBudgetFloor)},
		// New fields with defaults
		{Key: "per_strategy_cooldown_secs", Value: "60"},
		{Key: "real_trading_enabled", Value: "false"},
		{Key: "kelly_fraction", Value: "0.25"},
		{Key: "paper_bankroll", Value: "1000"},
		{Key: "real_bankroll", Value: "1000"},
		{Key: "real_order_time_in_force", Value: "immediate_or_cancel"},
		{Key: "real_order_timeout_s", Value: "10"},
	}
	return pairs
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
