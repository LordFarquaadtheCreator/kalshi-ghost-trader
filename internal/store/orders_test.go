package store

import (
	"context"
	"testing"
)

// TestDenormalizeResultToOrders seeds a market + paper orders, settles the
// market, and verifies that orders.result and orders.settled_ts are populated
// for every order on that market. Covers the WS "settled" path; the
// "determined" and reconciler paths share the same helper.
func TestDenormalizeResultToOrders(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, err := db.UpsertEventCheckNew(ctx, Event{
		EventTicker:  "EVT-DENORM",
		SeriesTicker: "KXATPMATCH",
		Title:        "Denorm Match",
		SubTitle:     "",
	})
	if err != nil {
		t.Fatalf("upsert event: %v", err)
	}

	mkt := Market{
		MarketTicker: "MKT-DENORM-A",
		EventTicker:  "EVT-DENORM",
		SeriesTicker: "KXATPMATCH",
		PlayerName:   "Player A",
		Status:       "active",
		OccurrenceTS: 1700000000000,
		CloseTS:      1700000100000,
	}
	if _, err := db.UpsertMarketCheckNew(ctx, mkt); err != nil {
		t.Fatalf("upsert market: %v", err)
	}

	orders := []Order{
		{TS: 1700000005000, MatchTicker: "EVT-DENORM", MarketTicker: "MKT-DENORM-A",
			Strategy: "matchpoint", Action: "buy", SuggestedSize: 5, MarketPrice: 0.40},
		{TS: 1700000006000, MatchTicker: "EVT-DENORM", MarketTicker: "MKT-DENORM-A",
			Strategy: "convexpool", Action: "buy", SuggestedSize: 3, MarketPrice: 0.55},
	}
	if err := db.InsertOrdersBatch(ctx, orders); err != nil {
		t.Fatalf("insert orders: %v", err)
	}

	// Settle the market directly — calling ApplyLifecycleEvent "settled"
	// would trigger P6 pruning (no ticks → coverage=none → cascade delete
	// the event and its orders). The denorm helper is what we're testing.
	if err := db.GormDB().WithContext(ctx).Model(&Market{}).
		Where("market_ticker = ?", "MKT-DENORM-A").
		Updates(map[string]any{
			"status":        "finalized",
			"result":        "yes",
			"settlement_ts": 1700000200000,
		}).Error; err != nil {
		t.Fatalf("settle market: %v", err)
	}
	if err := db.DenormalizeResultToOrders(ctx, "MKT-DENORM-A", "yes", 1700000200000); err != nil {
		t.Fatalf("DenormalizeResultToOrders: %v", err)
	}

	var got []Order
	if err := db.GormDB().WithContext(ctx).
		Where("market_ticker = ?", "MKT-DENORM-A").
		Order("ts").Find(&got).Error; err != nil {
		t.Fatalf("read orders: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d orders, want 2", len(got))
	}
	for _, o := range got {
		if o.Result != "yes" {
			t.Errorf("order %d result = %q, want %q", o.ID, o.Result, "yes")
		}
		if o.SettledTS != 1700000200000 {
			t.Errorf("order %d settled_ts = %d, want 1700000200000", o.ID, o.SettledTS)
		}
	}

	// Idempotent: running denorm directly must not change rows or error.
	if err := db.DenormalizeResultToOrders(ctx, "MKT-DENORM-A", "yes", 1700000200000); err != nil {
		t.Fatalf("denorm idempotent call: %v", err)
	}
	var got2 []Order
	if err := db.GormDB().WithContext(ctx).
		Where("market_ticker = ?", "MKT-DENORM-A").
		Order("ts").Find(&got2).Error; err != nil {
		t.Fatalf("read orders after idempotent call: %v", err)
	}
	for _, o := range got2 {
		if o.Result != "yes" || o.SettledTS != 1700000200000 {
			t.Errorf("order %d mutated by idempotent denorm: result=%q settled_ts=%d",
				o.ID, o.Result, o.SettledTS)
		}
	}

	// Empty result is a no-op.
	if err := db.DenormalizeResultToOrders(ctx, "MKT-DENORM-A", "", 0); err != nil {
		t.Fatalf("denorm empty result: %v", err)
	}
}
