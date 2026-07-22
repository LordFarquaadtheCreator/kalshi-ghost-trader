package paperorderinsights

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T, seed func(*gorm.DB)) *store.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := gdb.AutoMigrate(
		&store.Order{}, &store.Market{}, &store.Event{},
		&store.PaperOrderInsightRow{}, &store.PaperOrderSummaryRow{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if seed != nil {
		seed(gdb)
	}
	return store.NewFromGorm(gdb, slog.Default())
}

// seedOrders inserts paper orders across strategies + price bands. Markets
// M1 (result=yes) and M2 (result=no) so wins/losses are deterministic.
func seedOrders(db *gorm.DB) {
	db.Create(&store.Event{EventTicker: "E1", Title: "Home vs Away", SeriesTicker: "KXATPMATCH"})
	db.Create(&store.Market{MarketTicker: "M1", EventTicker: "E1", PlayerName: "Home", Status: "finalized", Result: "yes", CloseTS: 1700000000, SettlementTS: 1700001000})
	db.Create(&store.Market{MarketTicker: "M2", EventTicker: "E1", PlayerName: "Away", Status: "finalized", Result: "no", CloseTS: 1700000000, SettlementTS: 1700001000})

	// Day 2023-11-14 UTC (ts 1700000000 = 2023-11-14T22:13:20Z).
	// matchpoint: 5 wins on M1 (price 0.10-0.15), 5 losses on M2 (price 0.40-0.50).
	for i := 0; i < 5; i++ {
		db.Create(&store.Order{
			TS: 1700000000 + int64(i), MatchTicker: "E1", MarketTicker: "M1",
			Context: "match-point", MarketPrice: 0.12, EdgeCents: 5,
			SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
		})
		db.Create(&store.Order{
			TS: 1700000100 + int64(i), MatchTicker: "E1", MarketTicker: "M2",
			Context: "match-point", MarketPrice: 0.45, EdgeCents: 3,
			SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
		})
	}
	// setpoint: 3 wins on M1 (price 0.20-0.30).
	for i := 0; i < 3; i++ {
		db.Create(&store.Order{
			TS: 1700000200 + int64(i), MatchTicker: "E1", MarketTicker: "M1",
			Context: "set-point", MarketPrice: 0.25, EdgeCents: 4,
			SuggestedSize: 10, Strategy: "setpoint", Action: "buy", Side: "open",
		})
	}
}

func TestComputeMissingEmpty(t *testing.T) {
	db := testDB(t, nil)
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	rows, err := db.GetAllPaperOrderInsights()
	if err != nil {
		t.Fatalf("GetAllPaperOrderInsights: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 insight rows, got %d", len(rows))
	}
	summaries, err := db.GetAllPaperOrderSummaries()
	if err != nil {
		t.Fatalf("GetAllPaperOrderSummaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestComputeMissingSingleDay(t *testing.T) {
	db := testDB(t, seedOrders)
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	rows, err := db.GetAllPaperOrderInsights()
	if err != nil {
		t.Fatalf("GetAllPaperOrderInsights: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected insight rows, got 0")
	}
	// matchpoint: 2 bands (0.10-0.15 wins, 0.40-0.50 losses). setpoint: 1 band (0.20-0.30).
	if len(rows) != 3 {
		t.Fatalf("expected 3 insight rows (2 matchpoint bands + 1 setpoint band), got %d", len(rows))
	}
	summaries, err := db.GetAllPaperOrderSummaries()
	if err != nil {
		t.Fatalf("GetAllPaperOrderSummaries: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries (matchpoint + setpoint), got %d", len(summaries))
	}
	// matchpoint: 10 signals, 5 wins, 5 losses.
	var mp *store.PaperOrderSummaryRow
	for i := range summaries {
		if summaries[i].Strategy == "matchpoint" {
			mp = &summaries[i]
		}
	}
	if mp == nil {
		t.Fatal("matchpoint summary missing")
	}
	if mp.TotalSignals != 10 || mp.Wins != 5 || mp.Losses != 5 {
		t.Fatalf("matchpoint summary: signals=%d wins=%d losses=%d, want 10/5/5", mp.TotalSignals, mp.Wins, mp.Losses)
	}
}

func TestComputeMissingIdempotent(t *testing.T) {
	db := testDB(t, seedOrders)
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("first ComputeMissing: %v", err)
	}
	rows1, _ := db.GetAllPaperOrderInsights()
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("second ComputeMissing: %v", err)
	}
	rows2, _ := db.GetAllPaperOrderInsights()
	if len(rows1) != len(rows2) {
		t.Fatalf("idempotent: row count changed %d -> %d", len(rows1), len(rows2))
	}
}

func TestComputeMissingNewDay(t *testing.T) {
	db := testDB(t, seedOrders)
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("first ComputeMissing: %v", err)
	}
	// Insert order on a new day (2023-11-15).
	db.GormDB().Create(&store.Order{
		TS: 1700086400, MatchTicker: "E1", MarketTicker: "M1",
		Context: "match-point", MarketPrice: 0.12, EdgeCents: 5,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
	})
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("second ComputeMissing: %v", err)
	}
	rows, _ := db.GetAllPaperOrderInsights()
	// Original 3 rows + 1 new day row (matchpoint 0.10-0.15).
	foundNewDay := false
	for _, r := range rows {
		if r.Day == "2023-11-15" {
			foundNewDay = true
		}
	}
	if !foundNewDay {
		t.Fatalf("new day not computed; rows=%d", len(rows))
	}
}

func TestPendingOrdersExcluded(t *testing.T) {
	db := testDB(t, seedOrders)
	gdb := db.GormDB()
	// Add a pending order (market with no result).
	gdb.Create(&store.Event{EventTicker: "E2", Title: "Pending Match"})
	gdb.Create(&store.Market{MarketTicker: "M3", EventTicker: "E2", PlayerName: "P3", Status: "active", Result: ""})
	gdb.Create(&store.Order{
		TS: 1700000300, MatchTicker: "E2", MarketTicker: "M3",
		Context: "match-point", MarketPrice: 0.30, EdgeCents: 2,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
	})
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	rows, _ := db.GetAllPaperOrderInsights()
	for _, r := range rows {
		// Pending order was at price 0.30 (band 0.20-0.30) on 2023-11-14.
		if r.Day == "2023-11-14" && r.Strategy == "matchpoint" && r.BandLabel == "0.20-0.30" {
			t.Fatalf("pending order leaked into insights: %+v", r)
		}
	}
}

func TestRealOrdersExcluded(t *testing.T) {
	db := testDB(t, seedOrders)
	gdb := db.GormDB()
	// Add a real order on resolved market.
	gdb.Create(&store.Order{
		TS: 1700000400, MatchTicker: "E1", MarketTicker: "M1",
		Context: "match-point", MarketPrice: 0.12, EdgeCents: 5,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
		IsReal: true,
	})
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	// matchpoint band 0.10-0.15 should still have 5 wins (not 6).
	rows, _ := db.GetAllPaperOrderInsights()
	for _, r := range rows {
		if r.Strategy == "matchpoint" && r.BandLabel == "0.10-0.15" {
			if r.N != 5 {
				t.Fatalf("real order leaked into insights: N=%d, want 5", r.N)
			}
		}
	}
}

func TestPriceBoundsExcluded(t *testing.T) {
	db := testDB(t, seedOrders)
	gdb := db.GormDB()
	// Orders at price extremes — should be excluded.
	gdb.Create(&store.Order{
		TS: 1700000500, MatchTicker: "E1", MarketTicker: "M1",
		Context: "match-point", MarketPrice: 0.005, EdgeCents: 5,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
	})
	gdb.Create(&store.Order{
		TS: 1700000600, MatchTicker: "E1", MarketTicker: "M1",
		Context: "match-point", MarketPrice: 0.995, EdgeCents: 5,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
	})
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	rows, _ := db.GetAllPaperOrderInsights()
	for _, r := range rows {
		if r.Strategy == "matchpoint" && (r.BandLo < 0.01 || r.BandHi > 0.99) {
			t.Fatalf("extreme-price order leaked into insights: %+v", r)
		}
	}
}

func TestCumPnLSeries(t *testing.T) {
	db := testDB(t, seedOrders)
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	summaries, _ := db.GetAllPaperOrderSummaries()
	var mp *store.PaperOrderSummaryRow
	for i := range summaries {
		if summaries[i].Strategy == "matchpoint" {
			mp = &summaries[i]
		}
	}
	if mp == nil {
		t.Fatal("matchpoint summary missing")
	}
	if mp.CumPnLJSON == "" || mp.CumPnLJSON == "[]" {
		t.Fatalf("cum_pnl_json empty: %q", mp.CumPnLJSON)
	}
	// 10 orders → 10 cum_pnl points.
	var pts int
	for _, c := range mp.CumPnLJSON {
		if c == '{' {
			pts++
		}
	}
	if pts != 10 {
		t.Fatalf("cum_pnl points: %d, want 10", pts)
	}
}

// Ensure cron doesn't block forever on a slow DB — sanity check elapsed.
func TestComputeMissingFast(t *testing.T) {
	db := testDB(t, seedOrders)
	start := time.Now()
	if err := ComputeMissing(db, slog.Default()); err != nil {
		t.Fatalf("ComputeMissing: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ComputeMissing too slow: %v", elapsed)
	}
}
