package algorithms

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// adOutTestSetup creates a strategy + collector + registers markets.
func adOutTestSetup(t *testing.T) (*AdOutStrategy, *OrderCollector) {
	t.Helper()
	col := NewOrderCollector()
	strat := NewAdOutStrategy(col, slog.Default(), DefaultAdOutConfig())
	strat.RegisterMarkets("E1", []string{"HOME-MKT", "AWAY-MKT"})
	return strat, col
}

// adOutPoint builds a store.Point at ad-out state.
// server=1 (home serving), away has "A" → returner is away (player 2).
func adOutPoint(server int, returnerHasAdv bool) store.Point {
	p := store.Point{
		MatchTicker:  "E1",
		TS:           time.Now().UnixMilli(),
		SetNumber:    1,
		GameNumber:   1,
		PointNumber:  5,
		Server:       server,
		HomePoints:   "40",
		AwayPoints:   "40",
		HomeGames:    4,
		AwayGames:    4,
	}
	if !returnerHasAdv {
		return p
	}
	if server == 1 {
		p.AwayPoints = "A" // away is returner, has advantage
	} else {
		p.HomePoints = "A" // home is returner, has advantage
	}
	return p
}

// nextPoint builds a point event after the ad-out (new point in same game
// or new game — doesn't matter, just a different point_number/ts).
func nextPoint(server, scorer int) store.Point {
	return store.Point{
		MatchTicker:  "E1",
		TS:           time.Now().Add(2 * time.Second).UnixMilli(),
		SetNumber:    1,
		GameNumber:   1,
		PointNumber:  6,
		Server:       server,
		Scorer:       scorer,
		HomePoints:   "40",
		AwayPoints:   "40",
		HomeGames:    4,
		AwayGames:    4,
	}
}

func TestAdOutBuyOnAdOut(t *testing.T) {
	strat, col := adOutTestSetup(t)
	// Set price for away market (returner when home serves).
	strat.OnPrice("AWAY-MKT", 0.50)

	// Home serving, away has advantage → ad-out, buy AWAY-MKT.
	strat.OnPoint("E1", adOutPoint(1, true))

	if len(col.orders) != 1 {
		t.Fatalf("expected 1 order (buy), got %d", len(col.orders))
	}
	o := col.orders[0]
	if o.Action != "buy" {
		t.Errorf("action = %v, want buy", o.Action)
	}
	if o.Side != store.OrderSideOpen {
		t.Errorf("side = %v, want open", o.Side)
	}
	if o.MarketTicker != "AWAY-MKT" {
		t.Errorf("market = %v, want AWAY-MKT", o.MarketTicker)
	}
	if o.MarketPrice != 0.50 {
		t.Errorf("price = %v, want 0.50", o.MarketPrice)
	}
	if o.Strategy != "adout" {
		t.Errorf("strategy = %v, want adout", o.Strategy)
	}
}

func TestAdOutBuyHomeReturner(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("HOME-MKT", 0.50)

	// Away serving, home has advantage → ad-out, buy HOME-MKT.
	strat.OnPoint("E1", adOutPoint(2, true))

	if len(col.orders) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.orders))
	}
	if col.orders[0].MarketTicker != "HOME-MKT" {
		t.Errorf("market = %v, want HOME-MKT", col.orders[0].MarketTicker)
	}
}

func TestAdOutNoBuyOnDeuce(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	// Deuce (40-40, no advantage) — no buy.
	strat.OnPoint("E1", adOutPoint(1, false))

	if len(col.orders) != 0 {
		t.Errorf("expected 0 orders on deuce, got %d", len(col.orders))
	}
}

func TestAdOutNoBuyOnAdIn(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("HOME-MKT", 0.50)

	// Ad-in: server has advantage (home serving, home has "A").
	p := adOutPoint(1, false)
	p.HomePoints = "A"
	strat.OnPoint("E1", p)

	if len(col.orders) != 0 {
		t.Errorf("expected 0 orders on ad-in, got %d", len(col.orders))
	}
}

func TestAdOutNoBuyTiebreak(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	p := adOutPoint(1, true)
	p.IsTiebreak = true
	strat.OnPoint("E1", p)

	if len(col.orders) != 0 {
		t.Errorf("expected 0 orders in tiebreak, got %d", len(col.orders))
	}
}

func TestAdOutNoBuyNoPrice(t *testing.T) {
	strat, col := adOutTestSetup(t)
	// No price set for the returner market.
	strat.OnPoint("E1", adOutPoint(1, true))

	if len(col.orders) != 0 {
		t.Errorf("expected 0 orders with no price, got %d", len(col.orders))
	}
}

func TestAdOutNoBuyEdgeTooSmall(t *testing.T) {
	strat, col := adOutTestSetup(t)
	// Price = 0.81 → edge = (0.82 - 0.81) * 100 = 1c < MinEdgeCents (3).
	strat.OnPrice("AWAY-MKT", 0.81)
	strat.OnPoint("E1", adOutPoint(1, true))

	if len(col.orders) != 0 {
		t.Errorf("expected 0 orders with thin edge, got %d", len(col.orders))
	}
}

func TestAdOutNoBuyPriceTooHigh(t *testing.T) {
	strat, col := adOutTestSetup(t)
	// Price = 0.90 > MaxMarketPrice (0.85).
	strat.OnPrice("AWAY-MKT", 0.90)
	strat.OnPoint("E1", adOutPoint(1, true))

	if len(col.orders) != 0 {
		t.Errorf("expected 0 orders with price > max, got %d", len(col.orders))
	}
}

func TestAdOutNoStacking(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	// First ad-out → buy.
	strat.OnPoint("E1", adOutPoint(1, true))
	if len(col.orders) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.orders))
	}

	// Second ad-out in same game (deuce → ad-out again) → no second buy.
	strat.OnPoint("E1", adOutPoint(1, true))
	if len(col.orders) != 1 {
		t.Errorf("expected still 1 order (no stacking), got %d", len(col.orders))
	}
}

func TestAdOutSellOnNextPoint(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	// Buy on ad-out.
	strat.OnPoint("E1", adOutPoint(1, true))
	if len(col.orders) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.orders))
	}

	// Price moved up (returner won the point, break).
	strat.OnPrice("AWAY-MKT", 0.65)

	// Next point event → sell.
	strat.OnPoint("E1", nextPoint(1, 2)) // server=1, scorer=2 (away won = break)
	if len(col.orders) != 2 {
		t.Fatalf("expected 2 orders (buy + sell), got %d", len(col.orders))
	}
	sell := col.orders[1]
	if sell.Action != "sell" {
		t.Errorf("sell action = %v, want sell", sell.Action)
	}
	if sell.Side != store.OrderSideClose {
		t.Errorf("sell side = %v, want close", sell.Side)
	}
	if sell.MarketTicker != "AWAY-MKT" {
		t.Errorf("sell market = %v, want AWAY-MKT", sell.MarketTicker)
	}
	if sell.MarketPrice != 0.65 {
		t.Errorf("sell price = %v, want 0.65", sell.MarketPrice)
	}
}

func TestAdOutNoSellOnSamePoint(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	// Buy on ad-out.
	p := adOutPoint(1, true)
	strat.OnPoint("E1", p)
	if len(col.orders) != 1 {
		t.Fatalf("expected 1 buy, got %d", len(col.orders))
	}

	// Same point replayed (same ts, set, game) → no sell.
	strat.OnPoint("E1", p)
	if len(col.orders) != 1 {
		t.Errorf("expected still 1 order (no sell on same point), got %d", len(col.orders))
	}
}

func TestAdOutSellThenRebuy(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	// Buy on ad-out.
	strat.OnPoint("E1", adOutPoint(1, true))
	// Sell on next point.
	strat.OnPrice("AWAY-MKT", 0.65)
	strat.OnPoint("E1", nextPoint(1, 2))
	if len(col.orders) != 2 {
		t.Fatalf("expected buy + sell, got %d", len(col.orders))
	}

	// Another ad-out later → can buy again.
	strat.OnPrice("AWAY-MKT", 0.55)
	p2 := adOutPoint(1, true)
	p2.TS = time.Now().Add(10 * time.Second).UnixMilli()
	p2.GameNumber = 2
	strat.OnPoint("E1", p2)
	if len(col.orders) != 3 {
		t.Errorf("expected 3rd order (rebuy after sell), got %d", len(col.orders))
	}
	if col.orders[2].Action != "buy" {
		t.Errorf("3rd order action = %v, want buy", col.orders[2].Action)
	}
}

func TestAdOutUnregisterClearsState(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)
	strat.OnPoint("E1", adOutPoint(1, true))
	if len(col.orders) != 1 {
		t.Fatal("expected buy")
	}

	strat.UnregisterMarkets("E1")

	// Re-register and send a point → no sell (state was cleared).
	strat.RegisterMarkets("E1", []string{"HOME-MKT", "AWAY-MKT"})
	strat.OnPrice("AWAY-MKT", 0.65)
	strat.OnPoint("E1", nextPoint(1, 2))
	if len(col.orders) != 1 {
		t.Errorf("expected still 1 order (state cleared on unregister), got %d", len(col.orders))
	}
}

func TestAdOutSellNoFreshPrice(t *testing.T) {
	strat, col := adOutTestSetup(t)
	strat.OnPrice("AWAY-MKT", 0.50)

	// Buy on ad-out.
	strat.OnPoint("E1", adOutPoint(1, true))

	// Delete price (simulates WS disconnect).
	strat.DeletePrice("AWAY-MKT")

	// Next point → sell skipped (no price), position left for settlement.
	strat.OnPoint("E1", nextPoint(1, 2))
	if len(col.orders) != 1 {
		t.Errorf("expected 1 order (sell skipped, no price), got %d", len(col.orders))
	}
}

func TestAdOutOnTickNoOp(t *testing.T) {
	strat, _ := adOutTestSetup(t)
	// Should not panic.
	strat.OnTick(context.Background())
}
