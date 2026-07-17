package algorithms

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

type testEnv struct {
	db     *store.DB
	tw     *store.TickWriter
	strat  *MatchPointStrategy
	ctx    context.Context
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.New(context.Background(), filepath.Join(dir, "test.db"), slog.Default())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	tw := db.NewTickWriter(100, 50, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tw.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		db.Close()
	})
	strat := NewMatchPointStrategy(NewTickWriterEmitter(tw), slog.Default(), 1000.0, 0.25)
	return &testEnv{db: db, tw: tw, strat: strat, ctx: ctx, cancel: cancel, wg: &wg}
}

func (e *testEnv) flushAndQueryOrders(t *testing.T) []store.Order {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	orders, err := e.db.GetOrders(context.Background())
	if err != nil {
		t.Fatalf("GetOrders: %v", err)
	}
	return orders
}

func makePoint(match string, setNum, gameNum, pointNum, server, scorer int,
	homePts, awayPts string, homeGames, awayGames int) store.Point {
	return store.Point{
		MatchTicker: match, SetNumber: setNum, GameNumber: gameNum, PointNumber: pointNum,
		Server: server, Scorer: scorer, HomePoints: homePts, AwayPoints: awayPts,
		HomeGames: homeGames, AwayGames: awayGames,
	}
}

func setMatchState(g *MatchPointStrategy, match string, setsHome, setsAway, lastSetNum int) {
	g.mu.Lock()
	g.matchStates[match] = &matchState{
		setsHome: setsHome, setsAway: setsAway, lastSetNum: lastSetNum,
	}
	g.mu.Unlock()
}

func TestDetectMatchPoint_HomeServing(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 1, 0, 2)
	p := makePoint("M1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	mp := g.detectMatchPoint(p)
	if mp == nil {
		t.Fatal("expected match point, got nil")
	}
	if mp.winner != 1 {
		t.Fatalf("winner=%d, want 1", mp.winner)
	}
	if mp.context != "home_match_point" {
		t.Fatalf("context=%q, want home_match_point", mp.context)
	}
}

func TestDetectMatchPoint_AwayServing(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 0, 1, 2)
	p := makePoint("M1", 2, 10, 1, 2, 0, "30", "40", 4, 5)
	mp := g.detectMatchPoint(p)
	if mp == nil {
		t.Fatal("expected match point, got nil")
	}
	if mp.winner != 2 {
		t.Fatalf("winner=%d, want 2", mp.winner)
	}
	if mp.context != "away_match_point" {
		t.Fatalf("context=%q, want away_match_point", mp.context)
	}
}

func TestDetectMatchPoint_NotOneSetAway(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 0, 0, 1)
	p := makePoint("M1", 1, 10, 1, 1, 0, "40", "30", 5, 4)
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil (0 sets won, needs 2), got match point")
	}
}

func TestDetectMatchPoint_AlreadyWon(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 2, 0, 3)
	p := makePoint("M1", 3, 1, 1, 1, 0, "40", "30", 5, 4)
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil (match over), got match point")
	}
}

func TestDetectMatchPoint_Tiebreak(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 1, 0, 2)
	p := makePoint("M1", 2, 13, 1, 1, 0, "40", "30", 6, 6)
	p.IsTiebreak = true
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil for tiebreak, got match point")
	}
}

func TestDetectMatchPoint_HomeNotLeadingGames(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 1, 0, 2)
	p := makePoint("M1", 2, 10, 1, 1, 0, "40", "30", 4, 4)
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil (games equal), got match point")
	}
}

func TestDetectMatchPoint_HomeNotAtFive(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 1, 0, 2)
	p := makePoint("M1", 2, 7, 1, 1, 0, "40", "30", 3, 2)
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil (gamesHome < 5), got match point")
	}
}

func TestDetectMatchPoint_Deuce(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 1, 0, 2)
	p := makePoint("M1", 2, 10, 1, 1, 0, "40", "40", 5, 4)
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil at deuce, got match point")
	}
}

func TestDetectMatchPoint_Advantage(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	setMatchState(g, "M1", 1, 0, 2)
	p := makePoint("M1", 2, 10, 1, 1, 0, "A", "40", 5, 4)
	mp := g.detectMatchPoint(p)
	if mp == nil {
		t.Fatal("expected match point at advantage, got nil")
	}
	if mp.winner != 1 {
		t.Fatalf("winner=%d, want 1", mp.winner)
	}
}

func TestCanWinGame(t *testing.T) {
	tests := []struct {
		name     string
		homePts  string
		awayPts  string
		server   int
		player   int
		expected bool
	}{
		{"advantage", "A", "40", 1, 1, true},
		{"forty_vs_thirty", "40", "30", 1, 1, true},
		{"forty_vs_forty", "40", "40", 1, 1, false},
		{"forty_vs_advantage", "40", "A", 1, 1, false},
		{"thirty", "30", "15", 1, 1, false},
		{"invalid_score", "XX", "15", 1, 1, false},
		{"away_advantage", "40", "A", 2, 2, true},
		{"away_forty_vs_thirty", "30", "40", 2, 2, true},
		{"away_forty_vs_forty", "40", "40", 2, 2, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := canWinGame(tc.homePts, tc.awayPts, tc.server, tc.player)
			if got != tc.expected {
				t.Fatalf("canWinGame(%q,%q,%d,%d)=%v, want %v",
					tc.homePts, tc.awayPts, tc.server, tc.player, got, tc.expected)
			}
		})
	}
}

func TestUpdateMatchState_SetTransition(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.updateMatchState(makePoint("M1", 1, 10, 1, 1, 1, "40", "30", 6, 4))
	g.updateMatchState(makePoint("M1", 2, 1, 1, 1, 0, "15", "0", 0, 0))
	g.mu.RLock()
	ms := g.matchStates["M1"]
	g.mu.RUnlock()
	if ms.setsHome != 1 {
		t.Fatalf("setsHome=%d, want 1", ms.setsHome)
	}
}

func TestUpdateMatchState_TiebreakSet(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.updateMatchState(makePoint("M1", 1, 13, 7, 1, 1, "40", "30", 7, 6))
	g.updateMatchState(makePoint("M1", 2, 1, 1, 1, 0, "15", "0", 0, 0))
	g.mu.RLock()
	ms := g.matchStates["M1"]
	g.mu.RUnlock()
	if ms.setsHome != 1 {
		t.Fatalf("tiebreak: setsHome=%d, want 1", ms.setsHome)
	}
}

func TestUpdateMatchState_FirstPoint(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.updateMatchState(makePoint("M1", 1, 1, 1, 1, 0, "15", "0", 0, 0))
	g.mu.RLock()
	ms := g.matchStates["M1"]
	g.mu.RUnlock()
	if ms.setsHome != 0 || ms.setsAway != 0 {
		t.Fatalf("first point: setsHome=%d setsAway=%d, want 0,0", ms.setsHome, ms.setsAway)
	}
}

func TestUpdateMatchState_MultipleSets(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.updateMatchState(makePoint("M1", 1, 10, 1, 1, 1, "40", "30", 6, 4))
	g.updateMatchState(makePoint("M1", 2, 9, 1, 1, 1, "40", "15", 6, 3))
	g.updateMatchState(makePoint("M1", 3, 1, 1, 1, 0, "15", "0", 0, 0))
	g.mu.RLock()
	ms := g.matchStates["M1"]
	g.mu.RUnlock()
	if ms.setsHome != 2 {
		t.Fatalf("setsHome=%d, want 2", ms.setsHome)
	}
	p := makePoint("M1", 3, 1, 1, 1, 0, "40", "30", 5, 4)
	if mp := g.detectMatchPoint(p); mp != nil {
		t.Fatal("expected nil after match won, got match point")
	}
}

func TestProcessPoint_FiresOrder(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.80)
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	o := orders[0]
	if o.Action != "buy" {
		t.Fatalf("action=%q, want buy", o.Action)
	}
	if o.ConvProb != 0.97 {
		t.Fatalf("convProb=%v, want 0.97", o.ConvProb)
	}
	if o.MarketPrice != 0.80 {
		t.Fatalf("marketPrice=%v, want 0.80", o.MarketPrice)
	}
	if o.EdgeCents != 17 {
		t.Fatalf("edgeCents=%d, want 17", o.EdgeCents)
	}
	// Kelly: 0.25 * (0.97-0.80)/(1-0.80) * 1000 = 0.25 * 0.85 * 1000 = 212.5
	if o.SuggestedSize < 212.49 || o.SuggestedSize > 212.51 {
		t.Fatalf("suggestedSize=%v, want ~212.5 (Kelly)", o.SuggestedSize)
	}
	if o.MarketTicker != "MKT-HOME" {
		t.Fatalf("marketTicker=%q, want MKT-HOME", o.MarketTicker)
	}
	if o.Context != "home_match_point" {
		t.Fatalf("context=%q, want home_match_point", o.Context)
	}
}

func TestProcessPoint_NotServing(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.80)
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 2, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (not serving), got %d", len(orders))
	}
}

func TestProcessPoint_NoPrice(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (no price), got %d", len(orders))
	}
}

func TestProcessPoint_StalePrice(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.80)
	e.strat.mu.Lock()
	e.strat.priceTimes["MKT-HOME"] = time.Now().Add(-61 * time.Second)
	e.strat.mu.Unlock()
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (stale price), got %d", len(orders))
	}
}

func TestProcessPoint_EdgeBelowThreshold(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.97)
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (edge < 1), got %d", len(orders))
	}
}

func TestProcessPoint_EdgeExactlyOne(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.96)
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (edge=1), got %d", len(orders))
	}
	if orders[0].EdgeCents != 1 {
		t.Fatalf("edgeCents=%d, want 1", orders[0].EdgeCents)
	}
}

func TestProcessPoint_Dedup(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-HOME", 0.80)
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (dedup), got %d", len(orders))
	}
}

func TestProcessPoint_MarketsNotRegistered(t *testing.T) {
	e := newTestEnv(t)
	e.strat.OnPrice("MKT-HOME", 0.80)
	setMatchState(e.strat, "EVT-1", 1, 0, 2)
	p := makePoint("EVT-1", 2, 10, 1, 1, 0, "40", "30", 5, 4)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (markets not registered), got %d", len(orders))
	}
}

func TestProcessPoint_AwayMatchPoint(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	e.strat.OnPrice("MKT-AWAY", 0.75)
	setMatchState(e.strat, "EVT-1", 0, 1, 2)
	p := makePoint("EVT-1", 2, 10, 1, 2, 0, "30", "40", 4, 5)
	e.strat.processPoint(p)
	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].MarketTicker != "MKT-AWAY" {
		t.Fatalf("marketTicker=%q, want MKT-AWAY", orders[0].MarketTicker)
	}
	if orders[0].Context != "away_match_point" {
		t.Fatalf("context=%q, want away_match_point", orders[0].Context)
	}
}

func TestSuggestedSize(t *testing.T) {
	tests := []struct {
		name     string
		edge     int
		expected float64
	}{
		{"min_edge", 1, 10.0},
		{"scales", 5, 50.0},
		{"capped", 10, 100.0},
		{"large_edge", 100, 100.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := suggestedSize(tc.edge)
			if got != tc.expected {
				t.Fatalf("suggestedSize(%d)=%v, want %v", tc.edge, got, tc.expected)
			}
		})
	}
}

func TestUnregisterMarkets_CleansAll(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.RegisterMarkets("EVT-1", []string{"MKT-HOME", "MKT-AWAY"})
	g.OnPrice("MKT-HOME", 0.80)
	g.OnPrice("MKT-AWAY", 0.20)
	setMatchState(g, "EVT-1", 1, 0, 2)
	g.UnregisterMarkets("EVT-1")
	g.mu.RLock()
	if _, ok := g.markets["EVT-1"]; ok {
		t.Fatal("markets not cleaned")
	}
	if _, ok := g.prices["MKT-HOME"]; ok {
		t.Fatal("price MKT-HOME not cleaned")
	}
	if _, ok := g.prices["MKT-AWAY"]; ok {
		t.Fatal("price MKT-AWAY not cleaned")
	}
	if _, ok := g.priceTimes["MKT-HOME"]; ok {
		t.Fatal("priceTime MKT-HOME not cleaned")
	}
	if _, ok := g.matchStates["EVT-1"]; ok {
		t.Fatal("matchState not cleaned")
	}
	if _, ok := g.seenPoints["EVT-1"]; ok {
		t.Fatal("seenPoints not cleaned")
	}
	g.mu.RUnlock()
}

func TestDeletePrice_RemovesSingle(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.OnPrice("MKT-A", 0.80)
	g.OnPrice("MKT-B", 0.50)
	g.DeletePrice("MKT-A")
	if g.GetPrice("MKT-A") != 0 {
		t.Fatal("MKT-A price not removed")
	}
	if g.GetPrice("MKT-B") != 0.50 {
		t.Fatal("MKT-B price should remain")
	}
}

func TestGetPriceAge_Missing(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	age := g.GetPriceAge("NONEXIST")
	if age < 30*time.Minute {
		t.Fatalf("age for missing market=%v, want >30min", age)
	}
}

func TestGetPriceAge_Fresh(t *testing.T) {
	g := NewMatchPointStrategy(nil, slog.Default(), 1000.0, 0.25)
	g.OnPrice("MKT-A", 0.80)
	age := g.GetPriceAge("MKT-A")
	if age > 1*time.Second {
		t.Fatalf("age for fresh price=%v, want <1s", age)
	}
}
