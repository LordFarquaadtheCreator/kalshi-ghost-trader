// Package config implements the immutable-snapshot configuration store.
//
// A Snapshot is a deeply immutable struct combining every field from v1's
// EnvConfig (YAML) and RuntimeConfig (app_config DB table). Readers call
// Current() and get a *Snapshot that is never mutated; updates build a fresh
// snapshot, swap the atomic pointer, and notify subscribers.
//
// Every app_config key is classified in keys.go as either readLive (read from
// Current() at use time, always fresh) or subscribed(topic) (a component
// rebuilds itself when the topic fires). The third state — silently stale —
// is structurally impossible because TestEveryKeyClassified asserts the
// classification covers exactly the set of keys the validator knows.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Snapshot is the immutable config snapshot. All fields are value types;
// slices are copied at build time. Never mutate a *Snapshot returned by
// Current() — treat it as read-only.
type Snapshot struct {
	// --- EnvConfig (from YAML, read once at startup) ---
	Environment       string
	APIKeyID          string
	PrivateKeyPath    string
	DBDSN             string
	MetricsAddr       string
	APITennisAPIKey   string
	DisableWSDataSave bool
	RESTBaseURL       string
	WSURL             string

	// --- RuntimeConfig (from app_config DB, refreshable) ---
	SeriesTickers []string

	ScanIntervalHours int
	TrackLeadMinutes  int

	WSMinBackoffSecs int
	WSMaxBackoffSecs int

	BatchSize      int
	FlushTimeoutMS int

	HTTPTimeoutSecs int
	RateLimitRPS    int

	SchedulerPollSecs int

	ReconcilerIntervalSecs      int
	OrderBackfillIntervalSecs   int
	ScheduleCheckerIntervalSecs int

	APITennisTimezone string

	KalshiLiveDataEnabled      bool
	KalshiLiveDataPollSecs     int
	KalshiLiveDataRateLimitRPS int

	CloseTimerEnabled  bool
	CloseTimerLeadMin  int
	CloseTimerMinPrice float64
	CloseTimerPollSecs int
	CloseTimerSize     float64

	OrderQuotaEnabled      bool
	OrderQuotaCooldownSecs int
	OrderQuotaMaxPerSec    int
	OrderQuotaBudgetTotal  float64
	OrderQuotaBudgetFloor  float64

	PerStrategyCooldownSecs int

	RealTradingEnabled bool
	KellyFraction      float64
	PaperBankroll      float64

	RealBankroll         float64
	RealOrderTimeInForce string
	RealOrderTimeoutS    int

	// v2 additions
	LegacySizing       bool
	RetentionWeeks     int
	InsightsRefreshSecs int
}

// envConfig is the YAML payload for app.yaml / app.dev.yaml.
type envConfig struct {
	Environment       string `yaml:"environment"`
	APIKeyID          string `yaml:"kalshi_api_key_id"`
	PrivateKeyPath    string `yaml:"kalshi_private_key_path"`
	DBDSN             string `yaml:"db_dsn"`
	MetricsAddr       string `yaml:"metrics_addr"`
	APITennisAPIKey   string `yaml:"apitennis_api_key"`
	DisableWSDataSave bool   `yaml:"disable_ws_data_save"`
	RESTBaseURL       string `yaml:"rest_base_url"`
	WSURL             string `yaml:"ws_url"`
}

// Store is the immutable-snapshot config store. Safe for concurrent use.
type Store struct {
	cur  atomic.Pointer[Snapshot]
	mu   sync.Mutex
	subs map[string][]chan<- *Snapshot
	db   *gorm.DB
}

// Load reads the YAML env config and the app_config DB table, builds the
// initial snapshot, and returns a ready Store. envPath is the path to
// app.yaml / app.dev.yaml.
func Load(ctx context.Context, db *gorm.DB, envPath string) (*Store, error) {
	env, err := loadEnv(envPath)
	if err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}
	pairs, err := getAllAppConfig(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("read app_config: %w", err)
	}
	snap := buildSnapshot(env, pairs)
	s := &Store{
		db:   db,
		subs: make(map[string][]chan<- *Snapshot),
	}
	s.cur.Store(snap)
	return s, nil
}

// Current returns the latest snapshot. Never nil after Load. Any goroutine,
// race-free by construction — the pointer is atomic.
func (s *Store) Current() *Snapshot {
	return s.cur.Load()
}

// Update validates the new value, writes it to app_config, rebuilds the
// snapshot, swaps, and notifies subscribers on the key's topic (if any).
func (s *Store) Update(ctx context.Context, key, value string) error {
	class, ok := keyClassification(key)
	if !ok {
		return fmt.Errorf("unknown config key %q — add to keys.go", key)
	}
	if err := validateKey(key, value, s.Current()); err != nil {
		return err
	}
	if err := setAppConfig(ctx, s.db, key, value); err != nil {
		return fmt.Errorf("write app_config: %w", err)
	}
	pairs, err := getAllAppConfig(ctx, s.db)
	if err != nil {
		return fmt.Errorf("re-read app_config after update: %w", err)
	}
	env := envFromSnapshot(s.Current())
	newSnap := buildSnapshot(env, pairs)
	old := s.cur.Swap(newSnap)
	if class.topic != "" {
		s.notify(class.topic, newSnap)
	}
	_ = old
	return nil
}

// Subscribe returns a channel that receives a fresh *Snapshot whenever a key
// in the given topic changes. The channel is buffered (1); slow subscribers
// miss intermediate updates but always get the latest. Topics: "series",
// "ratelimit", "backoff", "gates", "retention", "insights".
func (s *Store) Subscribe(topic string) <-chan *Snapshot {
	ch := make(chan *Snapshot, 1)
	s.mu.Lock()
	s.subs[topic] = append(s.subs[topic], ch)
	s.mu.Unlock()
	return ch
}

func (s *Store) notify(topic string, snap *Snapshot) {
	s.mu.Lock()
	subs := s.subs[topic]
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- snap:
		default:
			// Subscriber's buffer is full — drop the intermediate update.
			// The next one carries the latest state. This is intentional:
			// subscribers rebuild from the snapshot they receive, so missing
			// an intermediate is safe; blocking the writer is not.
		}
	}
}

// envFromSnapshot extracts the env portion of a snapshot for rebuilds.
func envFromSnapshot(s *Snapshot) *envConfig {
	return &envConfig{
		Environment:       s.Environment,
		APIKeyID:          s.APIKeyID,
		PrivateKeyPath:    s.PrivateKeyPath,
		DBDSN:             s.DBDSN,
		MetricsAddr:       s.MetricsAddr,
		APITennisAPIKey:   s.APITennisAPIKey,
		DisableWSDataSave: s.DisableWSDataSave,
		RESTBaseURL:       s.RESTBaseURL,
		WSURL:             s.WSURL,
	}
}

func loadEnv(path string) (*envConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c envConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.validate(path); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *envConfig) validate(path string) error {
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

// buildSnapshot constructs an immutable Snapshot from the env config and the
// app_config key-value pairs. Slices are copied.
func buildSnapshot(env *envConfig, pairs map[string]string) *Snapshot {
	s := &Snapshot{
		Environment:       env.Environment,
		APIKeyID:          env.APIKeyID,
		PrivateKeyPath:    env.PrivateKeyPath,
		DBDSN:             env.DBDSN,
		MetricsAddr:       env.MetricsAddr,
		APITennisAPIKey:   env.APITennisAPIKey,
		DisableWSDataSave: env.DisableWSDataSave,
		RESTBaseURL:       env.RESTBaseURL,
		WSURL:             env.WSURL,
	}
	applyRuntimeKeys(s, pairs)
	return s
}

// applyRuntimeKeys populates the runtime portion of a snapshot from the
// app_config key-value map. Ported verbatim from v1 runtimeconfig.applyFromMap.
func applyRuntimeKeys(s *Snapshot, m map[string]string) {
	s.SeriesTickers = parseJSONStringArray(m["series_tickers"])

	s.ScanIntervalHours = atoi(m["scan_interval_hours"])
	s.TrackLeadMinutes = atoi(m["track_lead_minutes"])
	s.WSMinBackoffSecs = atoi(m["ws_min_backoff_secs"])
	s.WSMaxBackoffSecs = atoi(m["ws_max_backoff_secs"])
	s.BatchSize = atoi(m["batch_size"])
	s.FlushTimeoutMS = atoi(m["flush_timeout_ms"])
	s.HTTPTimeoutSecs = atoi(m["http_timeout_secs"])
	s.RateLimitRPS = atoi(m["rate_limit_rps"])
	s.SchedulerPollSecs = atoi(m["scheduler_poll_secs"])

	s.APITennisTimezone = m["apitennis_timezone"]

	s.KalshiLiveDataEnabled = atob(m["kalshi_livedata_enabled"])
	s.KalshiLiveDataPollSecs = atoi(m["kalshi_livedata_poll_secs"])
	s.KalshiLiveDataRateLimitRPS = atoi(m["kalshi_livedata_rate_limit_rps"])

	s.CloseTimerEnabled = atob(m["close_timer_enabled"])
	s.CloseTimerLeadMin = atoi(m["close_timer_lead_min"])
	s.CloseTimerMinPrice = atof(m["close_timer_min_price"])
	s.CloseTimerPollSecs = atoi(m["close_timer_poll_secs"])
	s.CloseTimerSize = atof(m["close_timer_size"])

	s.ReconcilerIntervalSecs = atoi(m["reconciler_interval_secs"])
	s.OrderBackfillIntervalSecs = atoi(m["order_backfill_interval_secs"])
	s.ScheduleCheckerIntervalSecs = atoi(m["schedule_checker_interval_secs"])

	s.OrderQuotaEnabled = atob(m["order_quota_enabled"])
	s.OrderQuotaCooldownSecs = atoi(m["order_quota_cooldown_secs"])
	s.OrderQuotaMaxPerSec = atoi(m["order_quota_max_per_sec"])
	s.OrderQuotaBudgetTotal = atof(m["order_quota_budget_total"])
	s.OrderQuotaBudgetFloor = atof(m["order_quota_budget_floor"])

	s.PerStrategyCooldownSecs = atoi(m["per_strategy_cooldown_secs"])

	s.RealTradingEnabled = atob(m["real_trading_enabled"])
	s.KellyFraction = atof(m["kelly_fraction"])
	s.PaperBankroll = atof(m["paper_bankroll"])

	s.RealBankroll = atof(m["real_bankroll"])
	s.RealOrderTimeInForce = m["real_order_time_in_force"]
	s.RealOrderTimeoutS = atoi(m["real_order_timeout_s"])

	s.LegacySizing = atob(m["legacy_sizing"])
	s.RetentionWeeks = atoi(m["retention_weeks"])
	s.InsightsRefreshSecs = atoi(m["insights_refresh_secs"])
}

// validateKey checks cross-field constraints before writing to DB.
// Ported from v1 runtimeconfig.validateKey.
func validateKey(key, value string, cur *Snapshot) error {
	lead := cur.CloseTimerLeadMin
	track := cur.TrackLeadMinutes
	enabled := cur.CloseTimerEnabled

	switch key {
	case "close_timer_lead_min":
		lead = atoi(value)
	case "track_lead_minutes":
		track = atoi(value)
	case "close_timer_enabled":
		enabled = atob(value)
	}

	if enabled && lead > track {
		return fmt.Errorf("close_timer_lead_min (%d) cannot exceed track_lead_minutes (%d) — WS not subscribed until T-track_lead",
			lead, track)
	}
	return nil
}

// --- DB operations ---

// appConfigRow mirrors the app_config table shape for GORM.
type appConfigRow struct {
	Key       string `gorm:"column:key;primaryKey"`
	Value     string `gorm:"column:value"`
	UpdatedTS int64  `gorm:"column:updated_ts"`
}

func (appConfigRow) TableName() string { return "app_config" }

// appConfigHistoryRow mirrors app_config_history.
type appConfigHistoryRow struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Key       string `gorm:"column:key"`
	OldValue  string `gorm:"column:old_value"`
	NewValue  string `gorm:"column:new_value"`
	Action    string `gorm:"column:action"`
	ChangedTS int64  `gorm:"column:changed_ts"`
}

func (appConfigHistoryRow) TableName() string { return "app_config_history" }

func getAllAppConfig(ctx context.Context, db *gorm.DB) (map[string]string, error) {
	var rows []appConfigRow
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m, nil
}

func setAppConfig(ctx context.Context, db *gorm.DB, key, value string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old string
		var existing appConfigRow
		err := tx.Where("key = ?", key).First(&existing).Error
		if err == nil {
			old = existing.Value
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UnixMilli()
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_ts"}),
		}).Create(&appConfigRow{Key: key, Value: value, UpdatedTS: now}).Error; err != nil {
			return err
		}
		return tx.Create(&appConfigHistoryRow{
			Key: key, OldValue: old, NewValue: value, Action: "set", ChangedTS: now,
		}).Error
	})
}

// --- helpers (ported from v1 runtimeconfig) ---

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
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				arr = append(arr, p)
			}
		}
	}
	return arr
}
