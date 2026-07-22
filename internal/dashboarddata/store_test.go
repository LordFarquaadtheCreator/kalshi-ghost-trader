package dashboarddata

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testLiveStore(t *testing.T, seed func(*gorm.DB)) *LiveStore {
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
	if err := db.AutoMigrate(
		&store.Event{}, &store.Market{}, &store.Tick{}, &store.Point{},
		&store.Order{}, &store.KalshiScore{}, &store.FlashscoreMatch{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if seed != nil {
		seed(db)
	}
	s, err := NewLiveStore(db, slog.Default())
	if err != nil {
		t.Fatalf("NewLiveStore: %v", err)
	}
	return s
}

func seedDashboardData(db *gorm.DB) {
	db.Create(&store.Event{EventTicker: "E1", Title: "Home vs Away", SeriesTicker: "KXATPMATCH"})
	db.Create(&store.Market{
		MarketTicker: "M1", EventTicker: "E1", PlayerName: "Home",
		Status: "finalized", Result: "yes", CloseTS: 1700000000, SettlementTS: 1700001000,
	})
	db.Create(&store.Market{
		MarketTicker: "M2", EventTicker: "E1", PlayerName: "Away",
		Status: "finalized", Result: "no", CloseTS: 1700000000, SettlementTS: 1700001000,
	})
	db.Create(&store.Order{
		TS: 1699990000, MatchTicker: "E1", MarketTicker: "M1",
		Context: "match-point", MarketPrice: 0.40, EdgeCents: 5,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
	})
	db.Create(&store.Order{
		TS: 1699990100, MatchTicker: "E1", MarketTicker: "M2",
		Context: "match-point", MarketPrice: 0.55, EdgeCents: 3,
		SuggestedSize: 10, Strategy: "matchpoint", Action: "buy", Side: "open",
	})
}

func TestEventTitle(t *testing.T) {
	s := testLiveStore(t, seedDashboardData)
	if s.EventTitle("E1") != "Home vs Away" {
		t.Errorf("EventTitle(E1) = %q, want \"Home vs Away\"", s.EventTitle("E1"))
	}
	if s.EventTitle("nonexistent") != "" {
		t.Errorf("EventTitle(unknown) = %q, want empty", s.EventTitle("nonexistent"))
	}
}

func TestEventOccurrenceTS(t *testing.T) {
	s := testLiveStore(t, func(db *gorm.DB) {
		seedDashboardData(db)
		db.Create(&store.Market{
			MarketTicker: "M3", EventTicker: "E1", PlayerName: "Home",
			Status: "finalized", Result: "yes", OccurrenceTS: 1699995000,
		})
	})
	occ, err := s.EventOccurrenceTS(context.Background(), []string{"E1"})
	if err != nil {
		t.Fatalf("EventOccurrenceTS: %v", err)
	}
	if occ["E1"] != 1699995000 {
		t.Errorf("occ[E1] = %d, want 1699995000", occ["E1"])
	}
}

func TestLatestTickTS(t *testing.T) {
	s := testLiveStore(t, func(db *gorm.DB) {
		seedDashboardData(db)
		db.Create(&store.Tick{MarketTicker: "M1", TS: 1699990000, Price: 0.40})
		db.Create(&store.Tick{MarketTicker: "M1", TS: 1699990200, Price: 0.45})
		db.Create(&store.Tick{MarketTicker: "M2", TS: 1699990100, Price: 0.55})
	})
	ts, err := s.LatestTickTS(context.Background(), []string{"E1"})
	if err != nil {
		t.Fatalf("LatestTickTS: %v", err)
	}
	if ts["E1"] != 1699990200 {
		t.Errorf("ts[E1] = %d, want 1699990200", ts["E1"])
	}
}

func TestLatestScoresFromPoints(t *testing.T) {
	s := testLiveStore(t, func(db *gorm.DB) {
		seedDashboardData(db)
		db.Create(&store.Point{
			MatchTicker: "E1", TS: 1699990050, SetNumber: 1, GameNumber: 1,
			PointNumber: 1, Server: 1, HomePoints: "15", AwayPoints: "0",
			HomeGames: 0, AwayGames: 0,
		})
	})
	scores, err := s.LatestScores(context.Background(), []string{"E1"})
	if err != nil {
		t.Fatalf("LatestScores: %v", err)
	}
	sc, ok := scores["E1"]
	if !ok {
		t.Fatal("no score for E1")
	}
	if sc.HomePoints != "15" {
		t.Errorf("HomePoints = %q, want \"15\"", sc.HomePoints)
	}
}

func TestGetPaperOrderSummary(t *testing.T) {
	s := testLiveStore(t, seedDashboardData)
	summary, err := s.GetPaperOrderSummary(context.Background())
	if err != nil {
		t.Fatalf("GetPaperOrderSummary: %v", err)
	}
	if summary.TotalOrders != 2 {
		t.Errorf("TotalOrders = %d, want 2", summary.TotalOrders)
	}
	if summary.Resolved != 2 {
		t.Errorf("Resolved = %d, want 2", summary.Resolved)
	}
	// M1 result=yes → win. M2 result=no → loss.
	if summary.Wins != 1 {
		t.Errorf("Wins = %d, want 1", summary.Wins)
	}
	if summary.Losses != 1 {
		t.Errorf("Losses = %d, want 1", summary.Losses)
	}
}

func TestGetPaperOrderStrategies(t *testing.T) {
	s := testLiveStore(t, func(db *gorm.DB) {
		seedDashboardData(db)
		db.Create(&store.Order{
			TS: 1699990200, MatchTicker: "E1", MarketTicker: "M1",
			Strategy: "setpoint", Action: "buy", Side: "open",
			MarketPrice: 0.30, SuggestedSize: 5,
		})
	})
	strats, err := s.GetPaperOrderStrategies(context.Background())
	if err != nil {
		t.Fatalf("GetPaperOrderStrategies: %v", err)
	}
	if len(strats) != 2 {
		t.Fatalf("strategies = %v, want 2", strats)
	}
	// Sorted
	if strats[0] != "matchpoint" || strats[1] != "setpoint" {
		t.Errorf("strategies = %v, want [matchpoint setpoint]", strats)
	}
}

func TestGetPaperOrdersPage(t *testing.T) {
	s := testLiveStore(t, seedDashboardData)
	orders, hasMore, next, err := s.GetPaperOrdersPage(context.Background(), nil, 10)
	if err != nil {
		t.Fatalf("GetPaperOrdersPage: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("orders = %d, want 2", len(orders))
	}
	if hasMore {
		t.Error("hasMore = true, want false")
	}
	if next != nil {
		t.Error("next != nil, want nil")
	}
	// Newest first: order at TS 1699990100 should be first
	if orders[0].TS != 1699990100 {
		t.Errorf("orders[0].TS = %d, want 1699990100 (newest first)", orders[0].TS)
	}
	// PnL: M1 (result=yes, buy at 0.40, size 10) → won → PnL = 10 * (1 - 0.40) = 6
	// M2 (result=no, buy at 0.55, size 10) → lost → PnL = -10 * 0.55 = -5.5
	for _, o := range orders {
		if o.MarketTicker == "M1" && o.PnL != 6.0 {
			t.Errorf("M1 PnL = %.2f, want 6.00", o.PnL)
		}
		if o.MarketTicker == "M2" && o.PnL != -5.5 {
			t.Errorf("M2 PnL = %.2f, want -5.50", o.PnL)
		}
	}
}

func TestGetPaperOrdersPagePagination(t *testing.T) {
	s := testLiveStore(t, seedDashboardData)
	// limit=1 → first page has 1 order, hasMore=true
	orders, hasMore, next, err := s.GetPaperOrdersPage(context.Background(), nil, 1)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("page 1 orders = %d, want 1", len(orders))
	}
	if !hasMore {
		t.Error("page 1 hasMore = false, want true")
	}
	if next == nil {
		t.Fatal("page 1 next = nil, want cursor")
	}
	// Page 2 using cursor
	orders2, hasMore2, _, err := s.GetPaperOrdersPage(context.Background(), next, 1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(orders2) != 1 {
		t.Fatalf("page 2 orders = %d, want 1", len(orders2))
	}
	if hasMore2 {
		t.Error("page 2 hasMore = true, want false")
	}
}

func TestGetOrderCountsByEvent(t *testing.T) {
	s := testLiveStore(t, seedDashboardData)
	counts, err := s.GetOrderCountsByEvent(context.Background())
	if err != nil {
		t.Fatalf("GetOrderCountsByEvent: %v", err)
	}
	if counts["E1"] != 2 {
		t.Errorf("counts[E1] = %d, want 2", counts["E1"])
	}
}

func TestGetPendingOrderCountsByEvent(t *testing.T) {
	s := testLiveStore(t, func(db *gorm.DB) {
		seedDashboardData(db)
		// Add an unresolved market + order
		db.Create(&store.Event{EventTicker: "E2", Title: "Other vs Other"})
		db.Create(&store.Market{
			MarketTicker: "M3", EventTicker: "E2", PlayerName: "Other",
			Status: "active", Result: "",
		})
		db.Create(&store.Order{
			TS: 1699990300, MatchTicker: "E2", MarketTicker: "M3",
			Strategy: "matchpoint", Action: "buy", Side: "open",
			MarketPrice: 0.30, SuggestedSize: 5,
		})
	})
	counts, err := s.GetPendingOrderCountsByEvent(context.Background())
	if err != nil {
		t.Fatalf("GetPendingOrderCountsByEvent: %v", err)
	}
	// E1 has 2 orders but both resolved (M1 result=yes, M2 result=no) → not pending
	// E2 has 1 order on M3 (result empty) → pending
	if _, ok := counts["E1"]; ok {
		t.Error("E1 should not be in pending counts (both resolved)")
	}
	if counts["E2"] != 1 {
		t.Errorf("counts[E2] = %d, want 1", counts["E2"])
	}
}

func TestGetPassedMatches(t *testing.T) {
	s := testLiveStore(t, seedDashboardData)
	matches, err := s.GetPassedMatches(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetPassedMatches: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if matches[0].EventTicker != "E1" {
		t.Errorf("EventTicker = %q, want E1", matches[0].EventTicker)
	}
	if matches[0].Winner != "Home" {
		t.Errorf("Winner = %q, want \"Home\"", matches[0].Winner)
	}
	if matches[0].OrderCount != 2 {
		t.Errorf("OrderCount = %d, want 2", matches[0].OrderCount)
	}
	// Net PnL: M1 win → +6, M2 loss → -5.5 = 0.5
	if matches[0].NetPnL != 0.5 {
		t.Errorf("NetPnL = %.2f, want 0.50", matches[0].NetPnL)
	}
}

func TestGetEventTickPrices(t *testing.T) {
	s := testLiveStore(t, func(db *gorm.DB) {
		seedDashboardData(db)
		db.Create(&store.Tick{MarketTicker: "M1", TS: 1699990000, Price: 0.40})
		db.Create(&store.Tick{MarketTicker: "M1", TS: 1699990100, Price: 0.45})
	})
	data, err := s.GetEventTickPrices(context.Background(), "E1")
	if err != nil {
		t.Fatalf("GetEventTickPrices: %v", err)
	}
	if data.Title != "Home vs Away" {
		t.Errorf("Title = %q, want \"Home vs Away\"", data.Title)
	}
	if len(data.Markets) != 2 {
		t.Fatalf("Markets = %d, want 2", len(data.Markets))
	}
	// Find M1 market
	for _, m := range data.Markets {
		if m.MarketTicker == "M1" {
			if len(m.Ticks) != 2 {
				t.Errorf("M1 ticks = %d, want 2", len(m.Ticks))
			}
		}
	}
	if len(data.Orders) != 2 {
		t.Errorf("Orders = %d, want 2", len(data.Orders))
	}
}

func TestGetEventTickPricesEmptyEvent(t *testing.T) {
	s := testLiveStore(t, nil)
	data, err := s.GetEventTickPrices(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetEventTickPrices: %v", err)
	}
	if len(data.Markets) != 0 {
		t.Errorf("Markets = %d, want 0 for unknown event", len(data.Markets))
	}
}
