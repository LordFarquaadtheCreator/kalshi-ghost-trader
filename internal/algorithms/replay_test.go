package algorithms

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func findLiveDB(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../../kalshi_tennis.db",
		filepath.Join(os.Getenv("HOME"), "kalshi-ghost-trader", "kalshi_tennis.db"),
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	t.Skip("kalshi_tennis.db not found — skipping replay tests")
	return ""
}

func openLiveDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.New(context.Background(), path, slog.Default())
	if err != nil {
		t.Fatalf("open live DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestReplay_MatchPointFromDB(t *testing.T) {
	dbPath := findLiveDB(t)
	liveDB := openLiveDB(t, dbPath)

	ctx := context.Background()

	mkts, err := liveDB.GetSettledMarkets(ctx, 20)
	if err != nil {
		t.Fatalf("GetSettledMarkets: %v", err)
	}

	byEvent := make(map[string][]store.Market)
	for _, m := range mkts {
		byEvent[m.EventTicker] = append(byEvent[m.EventTicker], m)
	}

	var candidates []string
	for evt, ms := range byEvent {
		if len(ms) >= 2 {
			n, err := liveDB.GetPointCount(ctx, evt)
			if err != nil {
				continue
			}
			if n > 50 {
				candidates = append(candidates, evt)
			}
		}
	}

	if len(candidates) == 0 {
		t.Skip("no matches with sufficient points in DB")
	}

	tested := 0
	for _, matchTicker := range candidates {
		if tested >= 3 {
			break
		}
		t.Run(matchTicker, func(t *testing.T) {
			pts, err := liveDB.GetPointsByMatch(ctx, matchTicker)
			if err != nil {
				t.Fatalf("GetPointsByMatch: %v", err)
			}
			if len(pts) < 20 {
				t.Skip("insufficient points")
			}

			mkts, err := liveDB.GetMarketsByEvent(ctx, matchTicker)
			if err != nil {
				t.Fatalf("GetMarketsByEvent: %v", err)
			}
			if len(mkts) < 2 {
				t.Skip("fewer than 2 markets")
			}

			e := newTestEnv(t)
			e.strat.RegisterMarkets(matchTicker, []string{mkts[0].MarketTicker, mkts[1].MarketTicker})
			e.strat.OnPrice(mkts[0].MarketTicker, 0.80)
			e.strat.OnPrice(mkts[1].MarketTicker, 0.20)

			e.strat.OnPoints(pts)

			orders := e.flushAndQueryOrders(t)

			for _, o := range orders {
				if o.Action != "buy" {
					t.Errorf("order action=%q, want buy", o.Action)
				}
				if o.ConvProb != 0.97 {
					t.Errorf("convProb=%v, want 0.97", o.ConvProb)
				}
				if o.EdgeCents < 1 {
					t.Errorf("edgeCents=%d, want >= 1", o.EdgeCents)
				}
				if o.MatchTicker != matchTicker {
					t.Errorf("matchTicker=%q, want %q", o.MatchTicker, matchTicker)
				}
				if o.MarketTicker != mkts[0].MarketTicker && o.MarketTicker != mkts[1].MarketTicker {
					t.Errorf("marketTicker=%q, not in registered markets", o.MarketTicker)
				}
			}

			t.Logf("match %s: %d points, %d orders emitted", matchTicker, len(pts), len(orders))
		})
		tested++
	}
}

func TestReplay_NoFalsePositives(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-NFP", []string{"MKT-H", "MKT-A"})
	e.strat.OnPrice("MKT-H", 0.80)
	e.strat.OnPrice("MKT-A", 0.20)

	pts := []store.Point{
		makePoint("EVT-NFP", 1, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-NFP", 1, 1, 2, 1, 1, "30", "0", 0, 0),
		makePoint("EVT-NFP", 1, 1, 3, 1, 1, "40", "0", 0, 0),
		makePoint("EVT-NFP", 1, 2, 1, 2, 0, "0", "15", 0, 0),
		makePoint("EVT-NFP", 1, 2, 2, 2, 1, "0", "30", 0, 0),
	}
	e.strat.OnPoints(pts)

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders (no match points), got %d", len(orders))
	}
}

func TestReplay_SetTransitionAccuracy(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-STA", []string{"MKT-H", "MKT-A"})
	e.strat.OnPrice("MKT-H", 0.80)
	e.strat.OnPrice("MKT-A", 0.20)

	pts := []store.Point{
		makePoint("EVT-STA", 1, 10, 1, 1, 1, "40", "30", 6, 4),
		makePoint("EVT-STA", 2, 1, 1, 2, 0, "0", "15", 0, 0),
		makePoint("EVT-STA", 2, 9, 1, 2, 2, "30", "40", 3, 6),
		makePoint("EVT-STA", 3, 1, 1, 1, 0, "15", "0", 0, 0),
		makePoint("EVT-STA", 3, 10, 1, 1, 0, "40", "30", 5, 4),
	}
	e.strat.OnPoints(pts)

	e.strat.mu.RLock()
	ms := e.strat.matchStates["EVT-STA"]
	e.strat.mu.RUnlock()
	if ms == nil {
		t.Fatal("no match state")
	}
	if ms.setsHome != 1 {
		t.Errorf("setsHome=%d, want 1", ms.setsHome)
	}
	if ms.setsAway != 1 {
		t.Errorf("setsAway=%d, want 1", ms.setsAway)
	}

	orders := e.flushAndQueryOrders(t)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].SetNumber != 3 {
		t.Fatalf("setNumber=%d, want 3", orders[0].SetNumber)
	}
	if orders[0].Context != "home_match_point" {
		t.Fatalf("context=%q, want home_match_point", orders[0].Context)
	}
}
