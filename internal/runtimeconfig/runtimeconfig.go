// Package runtimeconfig manages runtime configuration loaded from the app_config DB table.
//
// RuntimeConfig holds all tunable parameters for the ghost-trader service.
// Values are read from the app_config table (seeded by SQL migrations) and
// can be updated at runtime via the CRUD methods on this struct.
//
// Env config (credentials, environment, URLs) lives in appconfig.EnvConfig.
// This package has no dependency on appconfig — complete separation.
package runtimeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RuntimeConfig holds all runtime configuration loaded from the app_config DB table.
// All mutations go through Update/UpdateBatch/Delete — no direct field writes from outside.
type RuntimeConfig struct {
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

	APITennisEnabled  bool
	APITennisTimezone string

	KalshiLiveDataEnabled  bool
	KalshiLiveDataPollSecs int

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

	db *store.DB
	mu sync.Mutex
}

// LoadFromDB reads runtime configuration from the app_config table
func LoadFromDB(db *store.DB) (*RuntimeConfig, error) {
	ctx := context.Background()
	pairs, err := getAll(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("read app_config: %w", err)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("app_config table is empty — run migrations to seed defaults")
	}

	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		m[kv.Key] = kv.Value
	}

	rc := &RuntimeConfig{db: db}
	rc.applyFromMap(m)
	return rc, nil
}

// Update writes a single key to app_config, validates, and reloads from DB.
func (rc *RuntimeConfig) Update(key, value string) error {
	if err := rc.validateKey(key, value); err != nil {
		return err
	}
	ctx := context.Background()
	if err := set(ctx, rc.db, key, value); err != nil {
		return err
	}
	return rc.readAndApply(ctx)
}

// UpdateBatch writes multiple keys and reloads from DB.
func (rc *RuntimeConfig) UpdateBatch(pairs []store.RuntimeConfig) error {
	for _, p := range pairs {
		if err := rc.validateKey(p.Key, p.Value); err != nil {
			return err
		}
	}
	ctx := context.Background()
	if err := setBatch(ctx, rc.db, pairs); err != nil {
		return err
	}
	return rc.readAndApply(ctx)
}

// Delete removes a key from app_config and reloads.
func (rc *RuntimeConfig) Delete(key string) error {
	ctx := context.Background()
	if err := deleteKey(ctx, rc.db, key); err != nil {
		return err
	}
	return rc.readAndApply(ctx)
}

// GetAll returns current config values from memory. No DB roundtrip.
func (rc *RuntimeConfig) GetAll() []store.RuntimeConfig {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return []store.RuntimeConfig{
		{Key: "series_tickers", Value: toJSONStringArray(rc.SeriesTickers)},
		{Key: "scan_interval_hours", Value: strconv.Itoa(rc.ScanIntervalHours)},
		{Key: "track_lead_minutes", Value: strconv.Itoa(rc.TrackLeadMinutes)},
		{Key: "ws_min_backoff_secs", Value: strconv.Itoa(rc.WSMinBackoffSecs)},
		{Key: "ws_max_backoff_secs", Value: strconv.Itoa(rc.WSMaxBackoffSecs)},
		{Key: "batch_size", Value: strconv.Itoa(rc.BatchSize)},
		{Key: "flush_timeout_ms", Value: strconv.Itoa(rc.FlushTimeoutMS)},
		{Key: "http_timeout_secs", Value: strconv.Itoa(rc.HTTPTimeoutSecs)},
		{Key: "rate_limit_rps", Value: strconv.Itoa(rc.RateLimitRPS)},
		{Key: "scheduler_poll_secs", Value: strconv.Itoa(rc.SchedulerPollSecs)},
		{Key: "apitennis_enabled", Value: strconv.FormatBool(rc.APITennisEnabled)},
		{Key: "apitennis_timezone", Value: rc.APITennisTimezone},
		{Key: "kalshi_livedata_enabled", Value: strconv.FormatBool(rc.KalshiLiveDataEnabled)},
		{Key: "kalshi_livedata_poll_secs", Value: strconv.Itoa(rc.KalshiLiveDataPollSecs)},
		{Key: "close_timer_enabled", Value: strconv.FormatBool(rc.CloseTimerEnabled)},
		{Key: "close_timer_lead_min", Value: strconv.Itoa(rc.CloseTimerLeadMin)},
		{Key: "close_timer_min_price", Value: strconv.FormatFloat(rc.CloseTimerMinPrice, 'f', -1, 64)},
		{Key: "close_timer_poll_secs", Value: strconv.Itoa(rc.CloseTimerPollSecs)},
		{Key: "close_timer_size", Value: strconv.FormatFloat(rc.CloseTimerSize, 'f', -1, 64)},
		{Key: "reconciler_interval_secs", Value: strconv.Itoa(rc.ReconcilerIntervalSecs)},
		{Key: "order_backfill_interval_secs", Value: strconv.Itoa(rc.OrderBackfillIntervalSecs)},
		{Key: "schedule_checker_interval_secs", Value: strconv.Itoa(rc.ScheduleCheckerIntervalSecs)},
		{Key: "order_quota_enabled", Value: strconv.FormatBool(rc.OrderQuotaEnabled)},
		{Key: "order_quota_cooldown_secs", Value: strconv.Itoa(rc.OrderQuotaCooldownSecs)},
		{Key: "order_quota_max_per_sec", Value: strconv.Itoa(rc.OrderQuotaMaxPerSec)},
		{Key: "order_quota_budget_total", Value: strconv.FormatFloat(rc.OrderQuotaBudgetTotal, 'f', -1, 64)},
		{Key: "order_quota_budget_floor", Value: strconv.FormatFloat(rc.OrderQuotaBudgetFloor, 'f', -1, 64)},
		{Key: "per_strategy_cooldown_secs", Value: strconv.Itoa(rc.PerStrategyCooldownSecs)},
		{Key: "real_trading_enabled", Value: strconv.FormatBool(rc.RealTradingEnabled)},
		{Key: "kelly_fraction", Value: strconv.FormatFloat(rc.KellyFraction, 'f', -1, 64)},
		{Key: "paper_bankroll", Value: strconv.FormatFloat(rc.PaperBankroll, 'f', -1, 64)},
		{Key: "real_bankroll", Value: strconv.FormatFloat(rc.RealBankroll, 'f', -1, 64)},
		{Key: "real_order_time_in_force", Value: rc.RealOrderTimeInForce},
		{Key: "real_order_timeout_s", Value: strconv.Itoa(rc.RealOrderTimeoutS)},
	}
}

// readAndApply re-reads all keys from DB and applies them. Called after writes.
func (rc *RuntimeConfig) readAndApply(ctx context.Context) error {
	pairs, err := getAll(ctx, rc.db)
	if err != nil {
		return fmt.Errorf("read app_config: %w", err)
	}
	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		m[kv.Key] = kv.Value
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.applyFromMap(m)
	return nil
}

// --- DB operations ---

func getAll(ctx context.Context, db *store.DB) ([]store.RuntimeConfig, error) {
	var pairs []store.RuntimeConfig
	err := db.GormDB().WithContext(ctx).Find(&pairs).Error
	return pairs, err
}

func set(ctx context.Context, db *store.DB, key, value string) error {
	return db.GormDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old string
		var existing store.RuntimeConfig
		err := tx.Where("key = ?", key).First(&existing).Error
		if err == nil {
			old = existing.Value
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		now := time.Now().UnixMilli()
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_ts"}),
		}).Create(&store.RuntimeConfig{Key: key, Value: value, UpdatedTS: now}).Error; err != nil {
			return err
		}
		return tx.Create(&store.RuntimeConfigHistory{
			Key: key, OldValue: old, NewValue: value, Action: "set",
			ChangedTS: now,
		}).Error
	})
}

func setBatch(ctx context.Context, db *store.DB, pairs []store.RuntimeConfig) error {
	if len(pairs) == 0 {
		return nil
	}
	return db.GormDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UnixMilli()
		var histRows []store.RuntimeConfigHistory
		for i := range pairs {
			var old string
			var existing store.RuntimeConfig
			err := tx.Where("key = ?", pairs[i].Key).First(&existing).Error
			if err == nil {
				old = existing.Value
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			pairs[i].UpdatedTS = now
			histRows = append(histRows, store.RuntimeConfigHistory{
				Key: pairs[i].Key, OldValue: old, NewValue: pairs[i].Value,
				Action: "set", ChangedTS: now,
			})
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_ts"}),
		}).CreateInBatches(&pairs, len(pairs)).Error; err != nil {
			return err
		}
		return tx.CreateInBatches(&histRows, len(histRows)).Error
	})
}

func deleteKey(ctx context.Context, db *store.DB, key string) error {
	return db.GormDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var kv store.RuntimeConfig
		err := tx.Where("key = ?", key).First(&kv).Error
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tx.Where("key = ?", key).Delete(&store.RuntimeConfig{}).Error; err != nil {
			return err
		}
		return tx.Create(&store.RuntimeConfigHistory{
			Key: key, OldValue: kv.Value, Action: "delete",
			ChangedTS: time.Now().UnixMilli(),
		}).Error
	})
}

// applyFromMap populates RuntimeConfig fields from a key-value map.
func (rc *RuntimeConfig) applyFromMap(m map[string]string) {
	rc.SeriesTickers = parseJSONStringArray(m["series_tickers"])

	rc.ScanIntervalHours = atoi(m["scan_interval_hours"])
	rc.TrackLeadMinutes = atoi(m["track_lead_minutes"])
	rc.WSMinBackoffSecs = atoi(m["ws_min_backoff_secs"])
	rc.WSMaxBackoffSecs = atoi(m["ws_max_backoff_secs"])
	rc.BatchSize = atoi(m["batch_size"])
	rc.FlushTimeoutMS = atoi(m["flush_timeout_ms"])
	rc.HTTPTimeoutSecs = atoi(m["http_timeout_secs"])
	rc.RateLimitRPS = atoi(m["rate_limit_rps"])
	rc.SchedulerPollSecs = atoi(m["scheduler_poll_secs"])

	rc.APITennisEnabled = atob(m["apitennis_enabled"])
	rc.APITennisTimezone = m["apitennis_timezone"]

	rc.KalshiLiveDataEnabled = atob(m["kalshi_livedata_enabled"])
	rc.KalshiLiveDataPollSecs = atoi(m["kalshi_livedata_poll_secs"])

	rc.CloseTimerEnabled = atob(m["close_timer_enabled"])
	rc.CloseTimerLeadMin = atoi(m["close_timer_lead_min"])
	rc.CloseTimerMinPrice = atof(m["close_timer_min_price"])
	rc.CloseTimerPollSecs = atoi(m["close_timer_poll_secs"])
	rc.CloseTimerSize = atof(m["close_timer_size"])

	rc.ReconcilerIntervalSecs = atoi(m["reconciler_interval_secs"])
	rc.OrderBackfillIntervalSecs = atoi(m["order_backfill_interval_secs"])
	rc.ScheduleCheckerIntervalSecs = atoi(m["schedule_checker_interval_secs"])

	rc.OrderQuotaEnabled = atob(m["order_quota_enabled"])
	rc.OrderQuotaCooldownSecs = atoi(m["order_quota_cooldown_secs"])
	rc.OrderQuotaMaxPerSec = atoi(m["order_quota_max_per_sec"])
	rc.OrderQuotaBudgetTotal = atof(m["order_quota_budget_total"])
	rc.OrderQuotaBudgetFloor = atof(m["order_quota_budget_floor"])

	rc.PerStrategyCooldownSecs = atoi(m["per_strategy_cooldown_secs"])

	rc.RealTradingEnabled = atob(m["real_trading_enabled"])
	rc.KellyFraction = atof(m["kelly_fraction"])
	rc.PaperBankroll = atof(m["paper_bankroll"])

	rc.RealBankroll = atof(m["real_bankroll"])
	rc.RealOrderTimeInForce = m["real_order_time_in_force"]
	rc.RealOrderTimeoutS = atoi(m["real_order_timeout_s"])
}

// validateKey checks cross-field constraints before writing to DB.
func (rc *RuntimeConfig) validateKey(key, value string) error {
	lead := rc.CloseTimerLeadMin
	track := rc.TrackLeadMinutes
	enabled := rc.CloseTimerEnabled

	switch key {
	case "close_timer_lead_min":
		lead = atoi(value)
	case "track_lead_minutes":
		track = atoi(value)
	case "close_timer_enabled":
		enabled = atob(value)
	}

	if enabled && lead > track {
		return fmt.Errorf("close_timer_lead_minutes (%d) cannot exceed track_lead_minutes (%d) — WS not subscribed until T-track_lead",
			lead, track)
	}
	return nil
}

// --- helpers ---

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

func toJSONStringArray(arr []string) string {
	b, err := json.Marshal(arr)
	if err != nil {
		return "[]"
	}
	return string(b)
}
