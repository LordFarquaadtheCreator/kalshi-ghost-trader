package store

import (
	"context"
	"testing"
)

func TestUpsertEventCheckNew(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	e := Event{
		EventTicker:      "KXATPMATCH-EVENT-001",
		SeriesTicker:     "KXATPMATCH",
		Title:            "Djokovic vs Alcaraz",
		SubTitle:         "Wimbledon Final",
		Competition:      "ATP Wimbledon",
		CompetitionScope: "Game",
		MutuallyExclusive: true,
	}

	isNew, err := db.UpsertEventCheckNew(ctx, e)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if !isNew {
		t.Fatal("first upsert should be new")
	}

	// Second time — not new
	e.Title = "Updated Title"
	isNew, err = db.UpsertEventCheckNew(ctx, e)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if isNew {
		t.Fatal("second upsert should not be new")
	}
}

func TestUpsertMarketCheckNew(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Parent event must exist (FK)
	_, err := db.UpsertEventCheckNew(ctx, Event{
		EventTicker:  "EVT-001",
		SeriesTicker: "KXATPMATCH",
		Title:        "Match",
		SubTitle:     "",
	})
	if err != nil {
		t.Fatalf("upsert event: %v", err)
	}

	m := Market{
		MarketTicker: "MKT-001",
		EventTicker:  "EVT-001",
		SeriesTicker: "KXATPMATCH",
		PlayerName:   "Djokovic",
		Status:       "open",
		OccurrenceTS: 1700000000000,
	}

	isNew, err := db.UpsertMarketCheckNew(ctx, m)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if !isNew {
		t.Fatal("first upsert should be new")
	}

	// Second time — not new, status updated
	m.Status = "closed"
	isNew, err = db.UpsertMarketCheckNew(ctx, m)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if isNew {
		t.Fatal("second upsert should not be new")
	}
}

func TestGetActiveMarkets(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _ = db.UpsertEventCheckNew(ctx, Event{
		EventTicker: "EVT-A", SeriesTicker: "KXATPMATCH", Title: "A", SubTitle: "",
	})

	_, _ = db.UpsertMarketCheckNew(ctx, Market{
		MarketTicker: "MKT-OPEN", EventTicker: "EVT-A", SeriesTicker: "KXATPMATCH",
		PlayerName: "P1", Status: "open", OccurrenceTS: 1700000000000,
	})
	_, _ = db.UpsertMarketCheckNew(ctx, Market{
		MarketTicker: "MKT-ACTIVE", EventTicker: "EVT-A", SeriesTicker: "KXATPMATCH",
		PlayerName: "P2", Status: "active", OccurrenceTS: 1700000001000,
	})
	_, _ = db.UpsertMarketCheckNew(ctx, Market{
		MarketTicker: "MKT-CLOSED", EventTicker: "EVT-A", SeriesTicker: "KXATPMATCH",
		PlayerName: "P3", Status: "closed", OccurrenceTS: 1700000002000,
	})

	markets, err := db.GetActiveMarkets(ctx)
	if err != nil {
		t.Fatalf("GetActiveMarkets: %v", err)
	}

	if len(markets) != 2 {
		t.Fatalf("got %d markets, want 2 (open + active)", len(markets))
	}

	for _, m := range markets {
		if m.Status != "open" && m.Status != "active" {
			t.Fatalf("unexpected status %q", m.Status)
		}
	}
}

func TestApplyLifecycleEvent_Activated(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _ = db.UpsertEventCheckNew(ctx, Event{
		EventTicker: "EVT-LC", SeriesTicker: "KXATPMATCH", Title: "LC", SubTitle: "",
	})
	_, _ = db.UpsertMarketCheckNew(ctx, Market{
		MarketTicker: "MKT-LC", EventTicker: "EVT-LC", SeriesTicker: "KXATPMATCH",
		PlayerName: "P", Status: "open", OccurrenceTS: 1700000000000,
	})

	err := db.ApplyLifecycleEvent(ctx, LifecycleEvent{
		MarketTicker: "MKT-LC",
		EventType:    "activated",
		OpenTS:       1700000005000,
	})
	if err != nil {
		t.Fatalf("ApplyLifecycleEvent: %v", err)
	}

	markets, _ := db.GetActiveMarkets(ctx)
	found := false
	for _, m := range markets {
		if m.MarketTicker == "MKT-LC" && m.Status == "active" {
			found = true
		}
	}
	if !found {
		t.Fatal("market not active after activated event")
	}
}

func TestInsertTickBatch(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	ticks := []Tick{
		{TS: 1700000000000, RecvTS: 1700000000001, MarketTicker: "X", MsgType: "ticker", Payload: "{}"},
		{TS: 1700000000002, RecvTS: 1700000000003, MarketTicker: "X", MsgType: "trade", Payload: "{}"},
	}

	if err := db.InsertTickBatch(ctx, ticks); err != nil {
		t.Fatalf("InsertTickBatch: %v", err)
	}

	// Empty batch — no error
	if err := db.InsertTickBatch(ctx, nil); err != nil {
		t.Fatalf("InsertTickBatch(nil): %v", err)
	}
}
