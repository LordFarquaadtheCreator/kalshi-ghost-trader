package algorithms

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func TestIntegration_MatchPointPipeline(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-INT", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.80)

	pts := []store.Point{
		makePoint("EVT-INT", 1, 10, 4, 1, 1, "40", "30", 6, 4),
		makePoint("EVT-INT", 2, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-INT", 2, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.strat.OnPoints(pts)

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
	e.strat.RegisterMarkets("EVT-PAY", []string{"MKT-H", "MKT-A"})
	e.strat.OnPrice("MKT-H", 0.80)

	pts := []store.Point{
		makePoint("EVT-PAY", 1, 10, 4, 1, 1, "40", "30", 6, 4),
		makePoint("EVT-PAY", 2, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-PAY", 2, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.strat.OnPoints(pts)

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
	e.strat.RegisterMarkets("EVT-FULL", []string{"MKT-H", "MKT-A"})
	e.strat.OnPrice("MKT-H", 0.80)
	e.strat.OnPrice("MKT-A", 0.20)

	pts := []store.Point{
		makePoint("EVT-FULL", 1, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-FULL", 1, 10, 4, 1, 1, "40", "30", 6, 4),
		makePoint("EVT-FULL", 2, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-FULL", 2, 9, 1, 1, 0, "40", "30", 4, 4),
		makePoint("EVT-FULL", 2, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.strat.OnPoints(pts)

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
