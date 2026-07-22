package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB opens a GORM connection to a per-test schema in the test Postgres
// and runs the app_config + app_config_history table creation via AutoMigrate.
// Skips when TEST_DB_DSN is unset.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping Postgres-backed config tests")
	}

	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_cfg_" + hex.EncodeToString(b[:])

	{
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatalf("connect for schema create: %v", err)
		}
		if _, err := conn.Exec(context.Background(),
			fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
			_ = conn.Close(context.Background())
			t.Fatalf("create schema: %v", err)
		}
		_ = conn.Close(context.Background())
	}

	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	if err := db.AutoMigrate(&appConfigRow{}, &appConfigHistoryRow{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Logf("connect for schema drop: %v", err)
			return
		}
		defer func() { _ = conn.Close(context.Background()) }()
		if _, err := conn.Exec(context.Background(),
			fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema)); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	return db
}

// seedAppConfig inserts a full set of app_config rows so Load succeeds.
func seedAppConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	keys := allKnownKeys()
	rows := make([]appConfigRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, appConfigRow{Key: k, Value: defaultFor(k)})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed app_config: %v", err)
	}
}

func defaultFor(key string) string {
	switch key {
	case "series_tickers":
		return `["KXATPMATCH"]`
	case "real_trading_enabled", "kalshi_livedata_enabled", "close_timer_enabled",
		"order_quota_enabled", "legacy_sizing":
		return "false"
	case "kelly_fraction":
		return "0.25"
	case "paper_bankroll", "real_bankroll":
		return "1000"
	case "close_timer_min_price", "close_timer_size":
		return "0.5"
	case "order_quota_budget_total", "order_quota_budget_floor":
		return "100"
	case "real_order_time_in_force":
		return "immediate_or_cancel"
	case "apitennis_timezone":
		return "UTC"
	default:
		return "60"
	}
}

// writeEnvYAML writes a minimal valid env config to a temp file and returns
// its path.
func writeEnvYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/app.test.yaml"
	content := `environment: demo
kalshi_api_key_id: test-key
kalshi_private_key_path: /tmp/key.pem
db_dsn: host=localhost user=kalshi dbname=test port=5432 sslmode=disable
metrics_addr: 127.0.0.1:6060
rest_base_url: https://demo-api.kalshi.co
ws_url: wss://demo-api.kalshi.co/ws
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write env yaml: %v", err)
	}
	return path
}

func TestLoadAndCurrent(t *testing.T) {
	db := testDB(t)
	seedAppConfig(t, db)
	envPath := writeEnvYAML(t)

	s, err := Load(context.Background(), db, envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap := s.Current()
	if snap == nil {
		t.Fatal("Current() returned nil after Load")
	}
	if snap.Environment != "demo" {
		t.Errorf("Environment = %q, want demo", snap.Environment)
	}
	if len(snap.SeriesTickers) != 1 || snap.SeriesTickers[0] != "KXATPMATCH" {
		t.Errorf("SeriesTickers = %v, want [KXATPMATCH]", snap.SeriesTickers)
	}
}

func TestUpdateSwapsSnapshot(t *testing.T) {
	db := testDB(t)
	seedAppConfig(t, db)
	envPath := writeEnvYAML(t)

	s, err := Load(context.Background(), db, envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	old := s.Current()
	if old.KellyFraction != 0.25 {
		t.Fatalf("initial KellyFraction = %v, want 0.25", old.KellyFraction)
	}

	if err := s.Update(context.Background(), "kelly_fraction", "0.50"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Old pointer unchanged — immutability.
	if old.KellyFraction != 0.25 {
		t.Errorf("old snapshot mutated: KellyFraction = %v, want 0.25", old.KellyFraction)
	}
	// New pointer has new value.
	cur := s.Current()
	if cur == old {
		t.Fatal("Current() returned same pointer after Update")
	}
	if cur.KellyFraction != 0.50 {
		t.Errorf("new KellyFraction = %v, want 0.50", cur.KellyFraction)
	}
}

func TestConcurrentReadDuringUpdate(t *testing.T) {
	db := testDB(t)
	seedAppConfig(t, db)
	envPath := writeEnvYAML(t)

	s, err := Load(context.Background(), db, envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const readers = 16
	const updates = 100

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	// Readers: continuously read Current() and check invariants.
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < updates*10; j++ {
				snap := s.Current()
				if snap == nil {
					t.Errorf("Current() = nil")
					return
				}
				if snap.KellyFraction < 0 || snap.KellyFraction > 1 {
					t.Errorf("KellyFraction = %v, want [0,1]", snap.KellyFraction)
				}
			}
		}()
	}

	// Updater: toggles kelly_fraction between 0.25 and 0.50.
	go func() {
		defer wg.Done()
		for j := 0; j < updates; j++ {
			val := "0.25"
			if j%2 == 0 {
				val = "0.50"
			}
			if err := s.Update(context.Background(), "kelly_fraction", val); err != nil {
				t.Errorf("Update: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

func TestSubscribeReceivesOnRelevantKey(t *testing.T) {
	db := testDB(t)
	seedAppConfig(t, db)
	envPath := writeEnvYAML(t)

	s, err := Load(context.Background(), db, envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ch := s.Subscribe(topicSeries)

	// Update a series_tickers key — should notify on "series" topic.
	if err := s.Update(context.Background(), "series_tickers", `["KXWTAMATCH"]`); err != nil {
		t.Fatalf("Update: %v", err)
	}

	select {
	case snap := <-ch:
		if len(snap.SeriesTickers) != 1 || snap.SeriesTickers[0] != "KXWTAMATCH" {
			t.Errorf("notified snapshot SeriesTickers = %v, want [KXWTAMATCH]", snap.SeriesTickers)
		}
	default:
		t.Fatal("did not receive notification on series topic")
	}
}

func TestEveryKeyClassified(t *testing.T) {
	known := allKnownKeys()
	classified := make(map[string]bool, len(keyClassifications))
	for k := range keyClassifications {
		classified[k] = true
	}

	missing := []string{}
	for _, k := range known {
		if !classified[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("keys missing from keyClassifications: %v", missing)
	}

	extra := []string{}
	for k := range keyClassifications {
		found := false
		for _, kk := range known {
			if kk == k {
				found = true
				break
			}
		}
		if !found {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		t.Errorf("keys in keyClassifications but not in allKnownKeys: %v", extra)
	}
}

func TestUpdateRejectsUnknownKey(t *testing.T) {
	db := testDB(t)
	seedAppConfig(t, db)
	envPath := writeEnvYAML(t)

	s, err := Load(context.Background(), db, envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = s.Update(context.Background(), "nonexistent_key", "1")
	if err == nil {
		t.Fatal("Update with unknown key succeeded, want error")
	}
}
