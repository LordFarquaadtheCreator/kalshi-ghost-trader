package signal

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// --- mock PriceLookup ---

type mockPriceLookup struct {
	prices map[string]float64
	ages   map[string]time.Duration
}

func newMockPriceLookup() *mockPriceLookup {
	return &mockPriceLookup{
		prices: make(map[string]float64),
		ages:   make(map[string]time.Duration),
	}
}

func (m *mockPriceLookup) GetPrice(marketTicker string) float64 {
	return m.prices[marketTicker]
}

func (m *mockPriceLookup) GetPriceAge(marketTicker string) time.Duration {
	if age, ok := m.ages[marketTicker]; ok {
		return age
	}
	return time.Hour // stale by default if not set
}

func (m *mockPriceLookup) setFresh(ticker string, price float64) {
	m.prices[ticker] = price
	m.ages[ticker] = 0
}

func (m *mockPriceLookup) setStale(ticker string, price float64) {
	m.prices[ticker] = price
	m.ages[ticker] = 61 * time.Second
}

// --- DB seed helper ---

func seedEventWithMarkets(t *testing.T, db *store.DB, eventTicker string,
	markets [][2]string, closeTS int64) {
	t.Helper()
	ctx := context.Background()
	_, err := db.UpsertEventCheckNew(ctx, store.Event{
		EventTicker: eventTicker, SeriesTicker: "KXATPMATCH", Title: "Test", SubTitle: "",
	})
	if err != nil {
		t.Fatalf("upsert event: %v", err)
	}
	for _, mk := range markets {
		_, err = db.UpsertMarketCheckNew(ctx, store.Market{
			MarketTicker: mk[0], EventTicker: eventTicker, SeriesTicker: "KXATPMATCH",
			PlayerName: mk[1], Status: "open", CloseTS: closeTS,
		})
		if err != nil {
			t.Fatalf("upsert market: %v", err)
		}
	}
}

func newCloseTimerTestEnv(t *testing.T) (*testEnv, *mockPriceLookup, *CloseTimer) {
	t.Helper()
	e := newTestEnv(t)
	pl := newMockPriceLookup()
	ct := NewCloseTimer(e.db, pl, e.tw, 10, 0.85, 50.0, slog.Default())
	return e, pl, ct
}

// --- scan tests ---

func TestCloseTimer_FiresOrder(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.90)
	pl.setFresh("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	o := orders[0]
	if o.Action != "buy" {
		t.Fatalf("action=%q, want buy", o.Action)
	}
	if o.MarketTicker != "MKT-A" {
		t.Fatalf("marketTicker=%q, want MKT-A (favorite)", o.MarketTicker)
	}
	if o.ConvProb != 0.95 {
		t.Fatalf("convProb=%v, want 0.95", o.ConvProb)
	}
	if o.MarketPrice != 0.90 {
		t.Fatalf("marketPrice=%v, want 0.90", o.MarketPrice)
	}
	if o.EdgeCents != 9 {
		t.Fatalf("edgeCents=%d, want 9", o.EdgeCents)
	}
	if o.SuggestedSize != 50.0 {
		t.Fatalf("suggestedSize=%v, want 50.0", o.SuggestedSize)
	}
	if o.Context != "close_timer_10m" {
		t.Fatalf("context=%q, want close_timer_10m", o.Context)
	}
	if o.SetNumber != 0 {
		t.Fatalf("setNumber=%d, want 0", o.SetNumber)
	}
}

func TestCloseTimer_DedupPerEvent(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.90)
	pl.setFresh("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	ct.scan(context.Background(), 30) // second scan
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (dedup), got %d", len(orders))
	}
}

func TestCloseTimer_TooFarFromClose(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(630 * time.Second).UnixMilli() // 10.5 min
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.90)
	pl.setFresh("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (too far), got %d", len(orders))
	}
}

func TestCloseTimer_Final60Seconds(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(30 * time.Second).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.90)
	pl.setFresh("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (final 60s), got %d", len(orders))
	}
}

func TestCloseTimer_OnlyOneMarket(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, // only 1 market
	}, closeTS)
	pl.setFresh("MKT-A", 0.90)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (need 2 markets), got %d", len(orders))
	}
}

func TestCloseTimer_NoPrice(t *testing.T) {
	e, _, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	// no prices set

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (no price), got %d", len(orders))
	}
}

func TestCloseTimer_StalePrice(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setStale("MKT-A", 0.90)
	pl.setStale("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (stale prices), got %d", len(orders))
	}
}

func TestCloseTimer_BelowMinPrice(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.70) // below 0.85
	pl.setFresh("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (below minPrice), got %d", len(orders))
	}
}

func TestCloseTimer_PicksHigherPriced(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.92)
	pl.setFresh("MKT-B", 0.80)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].MarketTicker != "MKT-A" {
		t.Fatalf("marketTicker=%q, want MKT-A (higher priced)", orders[0].MarketTicker)
	}
}

func TestCloseTimer_OneStaleOneFresh(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setStale("MKT-A", 0.95) // stale but higher
	pl.setFresh("MKT-B", 0.86) // fresh, lower but above threshold

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].MarketTicker != "MKT-B" {
		t.Fatalf("marketTicker=%q, want MKT-B (fresh favorite)", orders[0].MarketTicker)
	}
}

func TestCloseTimer_OrderFields(t *testing.T) {
	e, pl, ct := newCloseTimerTestEnv(t)
	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-1", [][2]string{
		{"MKT-A", "Player A"}, {"MKT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-A", 0.90)
	pl.setFresh("MKT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	o := orders[0]
	if o.MatchTicker != "EVT-1" {
		t.Fatalf("matchTicker=%q, want EVT-1", o.MatchTicker)
	}
	if o.Action != "buy" {
		t.Fatalf("action=%q, want buy", o.Action)
	}
	if o.ConvProb != 0.95 {
		t.Fatalf("convProb=%v, want 0.95", o.ConvProb)
	}
	if o.SetNumber != 0 {
		t.Fatalf("setNumber=%d, want 0", o.SetNumber)
	}
}

// --- cleanupFired tests ---

func TestCleanupFired_EvictsSettled(t *testing.T) {
	ct := &CloseTimer{fired: make(map[string]bool)}
	ct.fired["EVT-SETTLED"] = true
	ct.fired["EVT-ACTIVE"] = true
	current := map[string][]store.Market{
		"EVT-ACTIVE": {{MarketTicker: "MKT-A"}},
	}
	ct.cleanupFired(current)
	if ct.fired["EVT-SETTLED"] {
		t.Fatal("EVT-SETTLED should be evicted")
	}
	if !ct.fired["EVT-ACTIVE"] {
		t.Fatal("EVT-ACTIVE should be retained")
	}
}

func TestCleanupFired_KeepsActive(t *testing.T) {
	ct := &CloseTimer{fired: make(map[string]bool)}
	ct.fired["EVT-1"] = true
	current := map[string][]store.Market{
		"EVT-1": {{MarketTicker: "MKT-A"}},
	}
	ct.cleanupFired(current)
	if !ct.fired["EVT-1"] {
		t.Fatal("EVT-1 should be retained")
	}
}

func TestCleanupFired_EmptyCurrent(t *testing.T) {
	ct := &CloseTimer{fired: make(map[string]bool)}
	ct.fired["EVT-1"] = true
	ct.fired["EVT-2"] = true
	ct.cleanupFired(map[string][]store.Market{})
	if len(ct.fired) != 0 {
		t.Fatalf("expected 0 fired entries, got %d", len(ct.fired))
	}
}
