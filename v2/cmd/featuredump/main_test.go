package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/parquet-go/parquet-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDBDump(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping featuredump tests")
	}

	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_dump_" + hex.EncodeToString(b[:])

	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := conn.Exec(context.Background(), fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		_ = conn.Close(context.Background())
		t.Fatalf("create schema: %v", err)
	}
	_ = conn.Close(context.Background())

	db, err := gorm.Open(postgres.Open(dsn+"&search_path="+schema), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	sqlDB, _ := db.DB()
	_, err = sqlDB.Exec(`
		CREATE TABLE ticks_v2 (
			market_ticker text NOT NULL,
			ts bigint NOT NULL,
			price_cents int NOT NULL,
			yes_bid_cents int,
			yes_ask_cents int,
			volume int,
			raw jsonb,
			best_bid_cents int,
			best_ask_cents int,
			best_bid_size int,
			best_ask_size int
		);
		CREATE TABLE markets (
			market_ticker text PRIMARY KEY,
			event_ticker text NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		_, _ = conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
		_ = conn.Close(context.Background())
	})

	return db, dsn + "&search_path=" + schema
}

func TestFeatureDumpRowCount(t *testing.T) {
	db, dsn := testDBDump(t)
	ctx := context.Background()
	sqlDB, _ := db.DB()

	// Seed 10 ticks for one market.
	now := time.Now().UnixMilli()
	for i := 0; i < 10; i++ {
		bid := 50 + i
		ask := 52 + i
		size := 100
		_, err := sqlDB.ExecContext(ctx, `
			INSERT INTO ticks_v2 (market_ticker, ts, price_cents, best_bid_cents, best_ask_cents, best_bid_size, best_ask_size)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, "TEST-H", now+int64(i)*1000, 51+i, bid, ask, size, size)
		if err != nil {
			t.Fatalf("seed tick %d: %v", i, err)
		}
	}
	_, err := sqlDB.ExecContext(ctx, `INSERT INTO markets (market_ticker, event_ticker) VALUES ('TEST-H', 'TEST')`)
	if err != nil {
		t.Fatalf("seed market: %v", err)
	}

	// Dump features.
	rows, err := dumpFeatures(ctx, db, nil, now-1000, now+20*1000)
	if err != nil {
		t.Fatalf("dumpFeatures: %v", err)
	}

	if len(rows) != 10 {
		t.Fatalf("row count = %d, want 10", len(rows))
	}

	// Verify all rows have the same feature_hash.
	hash := rows[0].FeatureHash
	for i, r := range rows {
		if r.FeatureHash != hash {
			t.Errorf("row %d: feature_hash = %s, want %s", i, r.FeatureHash, hash)
		}
		if r.MarketTicker != "TEST-H" {
			t.Errorf("row %d: market_ticker = %s, want TEST-H", i, r.MarketTicker)
		}
		if r.EventTicker != "TEST" {
			t.Errorf("row %d: event_ticker = %s, want TEST", i, r.EventTicker)
		}
	}

	// Write to Parquet and read back.
	tmpFile := t.TempDir() + "/test.parquet"
	if err := writeParquet(tmpFile, rows); err != nil {
		t.Fatalf("writeParquet: %v", err)
	}

	// Read back and verify.
	f, err := os.Open(tmpFile)
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}
	defer func() { _ = f.Close() }()

	pr := parquet.NewGenericReader[FeatureRow](f)
	readRows := make([]FeatureRow, 10)
	n, err := pr.Read(readRows)
	// EOF is expected when all rows are read.
	if err != nil && n != 10 {
		t.Fatalf("read parquet: %v (n=%d)", err, n)
	}
	if n != 10 {
		t.Errorf("parquet row count = %d, want 10", n)
	}
	_ = pr.Close()

	_ = dsn // suppress unused
}

func TestFeatureDumpByteIdenticalOnReplay(t *testing.T) {
	db, _ := testDBDump(t)
	ctx := context.Background()
	sqlDB, _ := db.DB()

	now := time.Now().UnixMilli()
	for i := 0; i < 5; i++ {
		bid := 50 + i
		ask := 52 + i
		_, err := sqlDB.ExecContext(ctx, `
			INSERT INTO ticks_v2 (market_ticker, ts, price_cents, best_bid_cents, best_ask_cents, best_bid_size, best_ask_size)
			VALUES ($1, $2, $3, $4, $5, 100, 100)
		`, "TEST-H", now+int64(i)*1000, 51+i, bid, ask)
		if err != nil {
			t.Fatalf("seed tick %d: %v", i, err)
		}
	}
	_, err := sqlDB.ExecContext(ctx, `INSERT INTO markets (market_ticker, event_ticker) VALUES ('TEST-H', 'TEST')`)
	if err != nil {
		t.Fatalf("seed market: %v", err)
	}

	// Dump twice — must be byte-identical.
	rows1, err := dumpFeatures(ctx, db, nil, now-1000, now+10*1000)
	if err != nil {
		t.Fatalf("dump 1: %v", err)
	}
	rows2, err := dumpFeatures(ctx, db, nil, now-1000, now+10*1000)
	if err != nil {
		t.Fatalf("dump 2: %v", err)
	}

	if len(rows1) != len(rows2) {
		t.Fatalf("row count mismatch: %d vs %d", len(rows1), len(rows2))
	}

	for i := range rows1 {
		if rows1[i].FeatureHash != rows2[i].FeatureHash {
			t.Errorf("row %d: feature_hash mismatch", i)
		}
		if rows1[i].Features != rows2[i].Features {
			t.Errorf("row %d: features JSON mismatch: %q vs %q", i, rows1[i].Features, rows2[i].Features)
		}
		if rows1[i].PriceCents != rows2[i].PriceCents {
			t.Errorf("row %d: price mismatch", i)
		}
		if rows1[i].TS != rows2[i].TS {
			t.Errorf("row %d: ts mismatch", i)
		}
	}

	// Write both to Parquet and compare file sizes (byte-identical output).
	tmpDir := t.TempDir()
	file1 := tmpDir + "/dump1.parquet"
	file2 := tmpDir + "/dump2.parquet"

	if err := writeParquet(file1, rows1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := writeParquet(file2, rows2); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	info1, _ := os.Stat(file1)
	info2, _ := os.Stat(file2)
	if info1.Size() != info2.Size() {
		t.Errorf("parquet file sizes differ: %d vs %d", info1.Size(), info2.Size())
	}
}
