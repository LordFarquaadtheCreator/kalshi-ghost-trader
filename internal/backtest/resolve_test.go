package backtest

import (
	"testing"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func mkOrder(market, action, side string, price, size float64, result string) store.Order {
	return store.Order{
		MatchTicker:   "E1",
		MarketTicker:  market,
		Action:        action,
		Side:          side,
		MarketPrice:   price,
		SuggestedSize: size,
		Context:       "ctx",
		SetNumber:     1,
		EdgeCents:     5,
	}
}

func mkMkt(ticker, result string) MarketRow {
	return MarketRow{MarketTicker: ticker, Result: result, Status: "finalized"}
}

func TestResolveBuyOnlyWins(t *testing.T) {
	mkts := []MarketRow{mkMkt("M1", "yes"), mkMkt("M2", "no")}
	raw := []store.Order{
		mkOrder("M1", "buy", store.OrderSideOpen, 0.40, 10, "yes"),
		mkOrder("M2", "buy", store.OrderSideOpen, 0.30, 10, "no"),
	}
	orders := resolveOrdersWithSells(raw, mkts, 0)
	if len(orders) != 2 {
		t.Fatalf("got %d orders, want 2", len(orders))
	}
	// M1 result=yes, buy YES at 0.40, won → PnL = 10 * (1 - 0.40) = 6
	if !orders[0].Won {
		t.Errorf("orders[0].Won = false, want true")
	}
	if orders[0].PnL != 6.0 {
		t.Errorf("orders[0].PnL = %.2f, want 6.00", orders[0].PnL)
	}
	// M2 result=no, buy YES at 0.30, lost → PnL = -10 * 0.30 = -3
	if orders[1].Won {
		t.Errorf("orders[1].Won = true, want false")
	}
	if orders[1].PnL != -3.0 {
		t.Errorf("orders[1].PnL = %.2f, want -3.00", orders[1].PnL)
	}
}

func TestResolveBuyNoAction(t *testing.T) {
	mkts := []MarketRow{mkMkt("M1", "no")}
	raw := []store.Order{
		mkOrder("M1", "buy_no", store.OrderSideOpen, 0.30, 10, "no"),
	}
	orders := resolveOrdersWithSells(raw, mkts, 0)
	if len(orders) != 1 {
		t.Fatalf("got %d orders, want 1", len(orders))
	}
	// buy_no + result=no → won → PnL = 10 * (1 - 0.30) = 7
	if !orders[0].Won {
		t.Errorf("Won = false, want true for buy_no + result=no")
	}
	if orders[0].PnL != 7.0 {
		t.Errorf("PnL = %.2f, want 7.00", orders[0].PnL)
	}
}

func TestResolveSellMatchesBuyFIFO(t *testing.T) {
	mkts := []MarketRow{mkMkt("M1", "yes")}
	raw := []store.Order{
		mkOrder("M1", "buy", store.OrderSideOpen, 0.30, 10, "yes"),
		mkOrder("M1", "buy", store.OrderSideOpen, 0.40, 10, "yes"),
		mkOrder("M1", "sell", store.OrderSideClose, 0.50, 15, "yes"),
	}
	orders := resolveOrdersWithSells(raw, mkts, 0)
	if len(orders) != 3 {
		t.Fatalf("got %d orders, want 3", len(orders))
	}
	// First buy (0.30, size 10) fully matched by sell. PnL zeroed.
	if orders[0].PnL != 0 {
		t.Errorf("orders[0].PnL = %.2f, want 0 (matched buy zeroed)", orders[0].PnL)
	}
	if orders[0].Won {
		t.Errorf("orders[0].Won = true, want false (matched)")
	}
	// Second buy (0.40, size 10) partially matched (5 of 10). PnL zeroed
	// because FIFO matching zeroes the whole buy entry when it enters the
	// queue — the sell's PnL carries the round-trip for the matched portion,
	// and the unmatched 5 contracts have no PnL contributor here.
	if orders[1].PnL != 0 {
		t.Errorf("orders[1].PnL = %.2f, want 0 (matched buy zeroed)", orders[1].PnL)
	}
	// Sell: matches 10 @ 0.30 + 5 @ 0.40, sells @ 0.50
	// PnL = (0.50 - 0.30) * 10 + (0.50 - 0.40) * 5 = 2.0 + 0.5 = 2.5
	if !orders[2].Won {
		t.Errorf("orders[2].Won = false, want true (profitable sell)")
	}
	wantSellPnL := (0.50-0.30)*10 + (0.50-0.40)*5
	if orders[2].PnL != wantSellPnL {
		t.Errorf("orders[2].PnL = %.4f, want %.4f", orders[2].PnL, wantSellPnL)
	}
	if orders[2].Side != "close" {
		t.Errorf("orders[2].Side = %q, want \"close\"", orders[2].Side)
	}
}

func TestResolveNakedShortSkipped(t *testing.T) {
	mkts := []MarketRow{mkMkt("M1", "yes")}
	raw := []store.Order{
		mkOrder("M1", "sell", store.OrderSideClose, 0.50, 10, "yes"),
	}
	orders := resolveOrdersWithSells(raw, mkts, 0)
	if len(orders) != 0 {
		t.Fatalf("got %d orders, want 0 (naked short skipped)", len(orders))
	}
}

func TestResolveUnresolvedMarketSkipped(t *testing.T) {
	mkts := []MarketRow{mkMkt("M1", "")} // empty result = unresolved
	raw := []store.Order{
		mkOrder("M1", "buy", store.OrderSideOpen, 0.40, 10, ""),
	}
	orders := resolveOrdersWithSells(raw, mkts, 0)
	if len(orders) != 0 {
		t.Fatalf("got %d orders, want 0 (unresolved market skipped)", len(orders))
	}
}

func TestResolveMinPriceFilter(t *testing.T) {
	mkts := []MarketRow{mkMkt("M1", "yes"), mkMkt("M2", "yes")}
	raw := []store.Order{
		mkOrder("M1", "buy", store.OrderSideOpen, 0.10, 10, "yes"),
		mkOrder("M2", "buy", store.OrderSideOpen, 0.50, 10, "yes"),
	}
	orders := resolveOrdersWithSells(raw, mkts, 0.20)
	if len(orders) != 1 {
		t.Fatalf("got %d orders, want 1 (minPrice filter)", len(orders))
	}
	if orders[0].Market != "M2" {
		t.Errorf("orders[0].Market = %q, want M2", orders[0].Market)
	}
}

func TestComputeSummaryEmpty(t *testing.T) {
	s := computeSummary(nil)
	if s.TotalSignals != 0 {
		t.Errorf("TotalSignals = %d, want 0", s.TotalSignals)
	}
	if s.WinRate != 0 {
		t.Errorf("WinRate = %.2f, want 0", s.WinRate)
	}
}

func TestComputeSummaryBasic(t *testing.T) {
	orders := []Order{
		{Won: true, PnL: 6.0, Size: 10, Price: 0.40, EdgeCents: 5},
		{Won: false, PnL: -3.0, Size: 10, Price: 0.30, EdgeCents: 3},
	}
	s := computeSummary(orders)
	if s.TotalSignals != 2 {
		t.Errorf("TotalSignals = %d, want 2", s.TotalSignals)
	}
	if s.Wins != 1 || s.Losses != 1 {
		t.Errorf("Wins=%d Losses=%d, want 1/1", s.Wins, s.Losses)
	}
	if s.WinRate != 50.0 {
		t.Errorf("WinRate = %.2f, want 50.0", s.WinRate)
	}
	wantPnL := 3.0
	if s.NetPnL != wantPnL {
		t.Errorf("NetPnL = %.2f, want %.2f", s.NetPnL, wantPnL)
	}
	wantInvested := 10*0.40 + 10*0.30 // 7.0
	if s.TotalInvested != wantInvested {
		t.Errorf("TotalInvested = %.2f, want %.2f", s.TotalInvested, wantInvested)
	}
	wantROI := wantPnL / wantInvested * 100
	if s.ROI != wantROI {
		t.Errorf("ROI = %.2f, want %.2f", s.ROI, wantROI)
	}
	// ProfitFactor = grossWin / grossLoss = 6 / 3 = 2
	if s.ProfitFactor != 2.0 {
		t.Errorf("ProfitFactor = %.2f, want 2.0", s.ProfitFactor)
	}
}

func TestComputeSummaryAllLosses(t *testing.T) {
	orders := []Order{
		{Won: false, PnL: -3.0, Size: 10, Price: 0.30, EdgeCents: 3},
	}
	s := computeSummary(orders)
	// grossLoss > 0 but grossWin = 0 → ProfitFactor = 0
	if s.ProfitFactor != 0 {
		t.Errorf("ProfitFactor = %.2f, want 0 (no wins)", s.ProfitFactor)
	}
}
