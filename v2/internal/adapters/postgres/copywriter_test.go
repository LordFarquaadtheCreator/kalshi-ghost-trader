package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDBPartition opens a GORM connection to a per-test schema and creates
// the partitioned ticks_v2 + orderbook_v2 tables with a default partition.
func testDBPartition(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping Postgres COPY tests")
	}

	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_copy_" + hex.EncodeToString(b[:])

	{
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if _, err := conn.Exec(context.Background(), fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
			_ = conn.Close(context.Background())
			t.Fatalf("create schema: %v", err)
		}
		_ = conn.Close(context.Background())
	}

	db, err := gorm.Open(postgres.Open(dsn+"&search_path="+schema), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	// Create partitioned tables + a default partition for the current week.
	sqlDB, _ := db.DB()
	_, err = sqlDB.Exec(`
		CREATE TABLE ticks_v2 (
			market_ticker text NOT NULL,
			ts bigint NOT NULL,
			price_cents int NOT NULL,
			yes_bid_cents int, yes_ask_cents int, volume int,
			raw jsonb
		) PARTITION BY RANGE (ts);
		CREATE INDEX idx_ticks_v2_market_ts ON ticks_v2 (market_ticker, ts);
		CREATE TABLE ticks_v2_default PARTITION OF ticks_v2 DEFAULT;
	`)
	if err != nil {
		t.Fatalf("create ticks_v2: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Errorf("cleanup: %v", err)
			return
		}
		_, _ = conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
		_ = conn.Close(context.Background())
	})

	return db
}

func TestCopyTicksRoundTrip(t *testing.T) {
	db := testDBPartition(t)
	cw := NewCopyWriter(db)
	ctx := context.Background()

	// Generate 10k tick rows.
	now := time.Now().UnixMilli()
	rows := make([]TickRow, 10000)
	for i := range rows {
		rows[i] = TickRow{
			MarketTicker: "TEST-MARKET",
			TS:           now + int64(i),
			PriceCents:   50 + (i % 50),
		}
	}

	if err := cw.CopyTicks(ctx, rows); err != nil {
		t.Fatalf("CopyTicks: %v", err)
	}

	// Verify count.
	var count int64
	db.Raw("SELECT count(*) FROM ticks_v2").Scan(&count)
	if count != 10000 {
		t.Errorf("row count = %d, want 10000", count)
	}

	// Verify first row.
	var firstTick struct {
		MarketTicker string
		TS           int64
		PriceCents   int
	}
	db.Raw("SELECT market_ticker, ts, price_cents FROM ticks_v2 ORDER BY ts LIMIT 1").Scan(&firstTick)
	if firstTick.MarketTicker != "TEST-MARKET" {
		t.Errorf("market_ticker = %s, want TEST-MARKET", firstTick.MarketTicker)
	}
	if firstTick.PriceCents != 50 {
		t.Errorf("price_cents = %d, want 50", firstTick.PriceCents)
	}
}

func TestCopyOrderbookRoundTrip(t *testing.T) {
	db := testDBPartition(t)
	cw := NewCopyWriter(db)
	ctx := context.Background()

	// Create orderbook_v2 table.
	sqlDB, _ := db.DB()
	_, err := sqlDB.Exec(`
		CREATE TABLE orderbook_v2 (
			market_ticker text NOT NULL,
			ts bigint NOT NULL,
			is_snapshot boolean NOT NULL DEFAULT false,
			price_cents int, delta int, side text,
			raw jsonb
		) PARTITION BY RANGE (ts);
		CREATE TABLE orderbook_v2_default PARTITION OF orderbook_v2 DEFAULT;
	`)
	if err != nil {
		t.Fatalf("create orderbook_v2: %v", err)
	}

	now := time.Now().UnixMilli()
	rows := make([]OrderbookRow, 100)
	for i := range rows {
		p := 50 + i
		rows[i] = OrderbookRow{
			MarketTicker: "TEST-MARKET",
			TS:           now + int64(i),
			IsSnapshot:   false,
			PriceCents:   &p,
			Side:         strPtr("buy"),
		}
	}

	if err := cw.CopyOrderbook(ctx, rows); err != nil {
		t.Fatalf("CopyOrderbook: %v", err)
	}

	var count int64
	db.Raw("SELECT count(*) FROM orderbook_v2").Scan(&count)
	if count != 100 {
		t.Errorf("row count = %d, want 100", count)
	}
}

func TestCopyTicksEmpty(t *testing.T) {
	db := testDBPartition(t)
	cw := NewCopyWriter(db)

	if err := cw.CopyTicks(context.Background(), nil); err != nil {
		t.Errorf("CopyTicks(nil) = %v, want nil", err)
	}
}

func strPtr(s string) *string { return &s }
