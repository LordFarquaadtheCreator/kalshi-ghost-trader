package backtest

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// testEngine opens an in-memory sqlite DB with the relevant tables migrated,
// then constructs an Engine via DI (NewEngine with injected gorm.DB).
// Each test gets a fresh DB via unique DSN to avoid cross-test contamination.
func testEngine(t *testing.T, seed func(*gorm.DB)) *Engine {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	// Migrate only the tables the loader queries.
	if err := db.AutoMigrate(
		&store.Event{}, &store.Market{}, &store.Tick{}, &store.Point{},
		&store.FlashscoreMatch{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if seed != nil {
		seed(db)
	}
	e, err := NewEngine(slog.Default(), db)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func seedFinalizedMatch(db *gorm.DB) {
	// Event: "Home vs Away"
	db.Create(&store.Event{
		EventTicker:  "E1",
		SeriesTicker: "KXATPMATCH",
		Title:        "Home vs Away",
	})
	// Two finalized markets
	db.Create(&store.Market{
		MarketTicker: "M1", EventTicker: "E1", PlayerName: "Home Player",
		Status: "finalized", Result: "yes", CloseTS: 1700000000,
	})
	db.Create(&store.Market{
		MarketTicker: "M2", EventTicker: "E1", PlayerName: "Away Player",
		Status: "finalized", Result: "no", CloseTS: 1700000000,
	})
	// Ticks for M1 (price) and M2 (price + dollar_volume)
	db.Create(&store.Tick{MarketTicker: "M1", TS: 1699990000, Price: 0.40})
	db.Create(&store.Tick{MarketTicker: "M1", TS: 1699990100, Price: 0.45})
	db.Create(&store.Tick{MarketTicker: "M2", TS: 1699990000, Price: 0.55, DollarVolume: 100})
	// Point for E1
	db.Create(&store.Point{
		MatchTicker: "E1", TS: 1699990050, SetNumber: 1, GameNumber: 1,
		PointNumber: 1, Server: 1, Scorer: 1, HomePoints: "15", AwayPoints: "0",
		HomeGames: 0, AwayGames: 0,
	})
}

func TestLoadFinalizedMarkets(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	if len(e.markets) != 1 {
		t.Fatalf("markets map size = %d, want 1", len(e.markets))
	}
	mkts := e.markets["E1"]
	if len(mkts) != 2 {
		t.Fatalf("E1 markets = %d, want 2", len(mkts))
	}
	// Both markets should be finalized
	for _, m := range mkts {
		if m.Status != "finalized" {
			t.Errorf("market %s status = %q, want finalized", m.MarketTicker, m.Status)
		}
	}
}

func TestLoadSkipsNonFinalizedMarkets(t *testing.T) {
	seed := func(db *gorm.DB) {
		seedFinalizedMatch(db)
		// Add a non-finalized market on a different event
		db.Create(&store.Event{EventTicker: "E2", Title: "Other vs Other"})
		db.Create(&store.Market{
			MarketTicker: "M3", EventTicker: "E2", PlayerName: "Other",
			Status: "active", Result: "",
		})
	}
	e := testEngine(t, seed)
	if _, ok := e.markets["E2"]; ok {
		t.Error("E2 should not be loaded (non-finalized)")
	}
	if len(e.markets) != 1 {
		t.Errorf("markets map size = %d, want 1 (only finalized)", len(e.markets))
	}
}

func TestLoadMarketCloseTs(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	if e.marketCloseTs["E1"] != 1700000000 {
		t.Errorf("closeTs[E1] = %d, want 1700000000", e.marketCloseTs["E1"])
	}
}

func TestLoadEventTitlesAndSeries(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	if e.eventTitles["E1"] != "Home vs Away" {
		t.Errorf("title[E1] = %q, want \"Home vs Away\"", e.eventTitles["E1"])
	}
	if e.eventSeries["E1"] != "KXATPMATCH" {
		t.Errorf("series[E1] = %q, want KXATPMATCH", e.eventSeries["E1"])
	}
}

func TestLoadTickPrices(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	m1Ticks := e.tickPrices["M1"]
	if len(m1Ticks) != 2 {
		t.Fatalf("M1 tick prices = %d, want 2", len(m1Ticks))
	}
	if m1Ticks[0].Price != 0.40 || m1Ticks[1].Price != 0.45 {
		t.Errorf("M1 prices = [%.2f, %.2f], want [0.40, 0.45]", m1Ticks[0].Price, m1Ticks[1].Price)
	}
}

func TestLoadTickVolumes(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	m2Vols := e.tickVolumes["M2"]
	if len(m2Vols) != 1 {
		t.Fatalf("M2 volumes = %d, want 1", len(m2Vols))
	}
	if m2Vols[0].DollarVolume != 100 {
		t.Errorf("M2 dollar_volume = %.0f, want 100", m2Vols[0].DollarVolume)
	}
}

func TestLoadPoints(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	pts := e.points["E1"]
	if len(pts) != 1 {
		t.Fatalf("E1 points = %d, want 1", len(pts))
	}
	if pts[0].HomePoints != "15" {
		t.Errorf("HomePoints = %q, want \"15\"", pts[0].HomePoints)
	}
}

func TestLoadEmptyDB(t *testing.T) {
	e := testEngine(t, nil)
	if len(e.markets) != 0 {
		t.Errorf("markets = %d, want 0", len(e.markets))
	}
	if len(e.tickPrices) != 0 {
		t.Errorf("tickPrices = %d, want 0", len(e.tickPrices))
	}
	if len(e.points) != 0 {
		t.Errorf("points = %d, want 0", len(e.points))
	}
}
