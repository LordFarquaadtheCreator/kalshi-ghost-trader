package signal

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func TestIntegration_MatchPointPipeline(t *testing.T) {
	e := newTestEnv(t)
	e.gen.RegisterMarkets("EVT-INT", []string{"MKT-HOME", "MKT-AWAY"})
	e.gen.UpdatePrice("MKT-HOME", 0.80)

	// Simulate a 2-set match where home wins set 1, then has match point in set 2
	pts := []store.Point{
		// Last point of set 1 (home wins 6-4)
		makePoint("EVT-INT", 1, 10, 4, 1, 1, "40", "30", 6, 4),
		// First point of set 2 (triggers set transition)
		makePoint("EVT-INT", 2, 1, 1, 1, 0, "15", "0", 0, 0),
		// Match point: set 2, home 5-4, 40-30, home serving
		makePoint("EVT-INT", 2, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.gen.OnPoints(pts)

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	o := orders[0]
	if o.MatchTicker != "EVT-INT" {
		t.Fatalf("matchTicker=%q, want EVT-INT", o.MatchTicker)
	}
	if o.MarketTicker != "MKT-HOME" {
		t.Fatalf("marketTicker=%q, want MKT-HOME", o.MarketTicker)
	}
	if o.Context != "home_match_point" {
		t.Fatalf("context=%q, want home_match_point", o.Context)
	}
	if o.SetNumber != 2 {
		t.Fatalf("setNumber=%d, want 2", o.SetNumber)
	}
}

func TestIntegration_CloseTimerPipeline(t *testing.T) {
	e := newTestEnv(t)
	pl := newMockPriceLookup()
	ct := NewCloseTimer(e.db, pl, e.tw, 10, 0.85, 50.0, slog.Default())

	closeTS := time.Now().Add(9 * time.Minute).UnixMilli()
	seedEventWithMarkets(t, e.db, "EVT-CT", [][2]string{
		{"MKT-CT-A", "Player A"}, {"MKT-CT-B", "Player B"},
	}, closeTS)
	pl.setFresh("MKT-CT-A", 0.90)
	pl.setFresh("MKT-CT-B", 0.50)

	ct.scan(context.Background(), 30)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].MatchTicker != "EVT-CT" {
		t.Fatalf("matchTicker=%q, want EVT-CT", orders[0].MatchTicker)
	}
	if orders[0].MarketTicker != "MKT-CT-A" {
		t.Fatalf("marketTicker=%q, want MKT-CT-A", orders[0].MarketTicker)
	}
}

func TestIntegration_TickWriterFlushesOrders(t *testing.T) {
	e := newTestEnv(t)
	o := store.Order{
		TS:            time.Now().UnixMilli(),
		MatchTicker:   "EVT-FLUSH",
		MarketTicker:  "MKT-FLUSH",
		Action:        "buy",
		Context:       "test",
		ConvProb:      0.97,
		MarketPrice:   0.80,
		EdgeCents:     17,
		SuggestedSize: 34.0,
		SetNumber:     2,
		Payload:       `{"test": true}`,
	}
	if !e.tw.IngestOrder(o) {
		t.Fatal("IngestOrder returned false")
	}

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	got := orders[0]
	if got.MatchTicker != "EVT-FLUSH" {
		t.Fatalf("matchTicker=%q, want EVT-FLUSH", got.MatchTicker)
	}
	if got.EdgeCents != 17 {
		t.Fatalf("edgeCents=%d, want 17", got.EdgeCents)
	}
	if got.SuggestedSize != 34.0 {
		t.Fatalf("suggestedSize=%v, want 34.0", got.SuggestedSize)
	}
}

func TestIntegration_OrderPayload(t *testing.T) {
	e := newTestEnv(t)
	e.gen.RegisterMarkets("EVT-PAY", []string{"MKT-H", "MKT-A"})
	e.gen.UpdatePrice("MKT-H", 0.80)

	pts := []store.Point{
		makePoint("EVT-PAY", 1, 10, 4, 1, 1, "40", "30", 6, 4),
		makePoint("EVT-PAY", 2, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-PAY", 2, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.gen.OnPoints(pts)

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(orders[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["serving"] != true {
		t.Fatal("payload missing serving=true")
	}
	if payload["set"] != float64(2) {
		t.Fatalf("payload set=%v, want 2", payload["set"])
	}
	if payload["server"] != float64(1) {
		t.Fatalf("payload server=%v, want 1", payload["server"])
	}
	if payload["home_games"] != float64(5) {
		t.Fatalf("payload home_games=%v, want 5", payload["home_games"])
	}
	if payload["away_games"] != float64(4) {
		t.Fatalf("payload away_games=%v, want 4", payload["away_games"])
	}
}

func TestIntegration_MatchPointFullMatch(t *testing.T) {
	e := newTestEnv(t)
	e.gen.RegisterMarkets("EVT-FULL", []string{"MKT-H", "MKT-A"})
	e.gen.UpdatePrice("MKT-H", 0.80)
	e.gen.UpdatePrice("MKT-A", 0.20)

	// Full 2-set match: home wins both sets
	// Set 1: home wins 6-4
	// Set 2: home wins 6-4 (match point at 5-4, 40-30)
	pts := []store.Point{
		// Set 1, early game — NOT a match point (setsHome=0)
		makePoint("EVT-FULL", 1, 1, 1, 1, 0, "15", "0", 0, 0),
		// Set 1, game 10 — home wins set (6-4)
		makePoint("EVT-FULL", 1, 10, 4, 1, 1, "40", "30", 6, 4),
		// Set 2 starts — triggers set transition (setsHome=1)
		makePoint("EVT-FULL", 2, 1, 1, 1, 0, "15", "0", 0, 0),
		// Set 2, game 9 — NOT a match point (home at 4-4, not leading)
		makePoint("EVT-FULL", 2, 9, 1, 1, 0, "40", "30", 4, 4),
		// Set 2, game 10 — match point (home 5-4, 40-30, serving)
		makePoint("EVT-FULL", 2, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.gen.OnPoints(pts)

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (only at match point), got %d", len(orders))
	}
	if orders[0].Context != "home_match_point" {
		t.Fatalf("context=%q, want home_match_point", orders[0].Context)
	}
	if orders[0].SetNumber != 2 {
		t.Fatalf("setNumber=%d, want 2", orders[0].SetNumber)
	}
}
