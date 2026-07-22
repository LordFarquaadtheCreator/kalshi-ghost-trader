package backtest

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// stubStrategy is a minimal ReplayStrategy for testing RunStrategy.
// It emits one buy order per market on the first OnPriceAt call.
type stubStrategy struct {
	emitter   algorithms.OrderEmitter
	label     string
	prices    map[string]float64
	replayTS  time.Time
	series    string
	surface   string
	volumes   map[string][]algorithms.TickVolume
	scoreSeen int
}

func newStubStrategy(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
	return &stubStrategy{
		emitter: em,
		label:   "stub",
		prices:  make(map[string]float64),
		volumes: make(map[string][]algorithms.TickVolume),
	}
}

func (s *stubStrategy) OnPrice(marketTicker string, price float64) {}

func (s *stubStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	if _, seen := s.prices[marketTicker]; !seen {
		s.prices[marketTicker] = price
		s.emitter.EmitOrder(store.Order{
			MatchTicker:   "E1",
			MarketTicker:  marketTicker,
			Action:        "buy",
			Side:          store.OrderSideOpen,
			MarketPrice:   price,
			SuggestedSize: 10,
			Context:       "stub-signal",
			Strategy:      s.label,
		})
	}
}

func (s *stubStrategy) SetReplayTime(ts time.Time)             { s.replayTS = ts }
func (s *stubStrategy) RegisterMarkets(string, []string)       {}
func (s *stubStrategy) UnregisterMarkets(string)               {}
func (s *stubStrategy) DeletePrice(string)                     {}
func (s *stubStrategy) OnTick(context.Context)                 {}
func (s *stubStrategy) SetSeriesTicker(_, series string)       { s.series = series }
func (s *stubStrategy) SetSurface(_, surface string)           { s.surface = surface }
func (s *stubStrategy) SetVolumeSeries(m string, v []algorithms.TickVolume) {
	s.volumes[m] = v
}
func (s *stubStrategy) OnPoint(string, store.Point) { s.scoreSeen++ }

func TestRunStrategyEmitsOrders(t *testing.T) {
	dsn := "file:TestRunStrategyEmitsOrders?mode=memory&cache=shared"
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
		&store.Event{}, &store.Market{}, &store.Tick{}, &store.Point{}, &store.FlashscoreMatch{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seedFinalizedMatch(db)

	e, err := NewEngine(slog.Default(), db)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Register stub strategy
	e.factories = map[string]StrategyFactory{
		"stub": newStubStrategy,
	}

	res, err := e.RunStrategy("stub", 0)
	if err != nil {
		t.Fatalf("RunStrategy: %v", err)
	}
	if res.MatchCount != 1 {
		t.Errorf("MatchCount = %d, want 1", res.MatchCount)
	}
	// Stub emits one order per market on first price tick. Two markets → 2 orders.
	if len(res.Orders) != 2 {
		t.Fatalf("Orders = %d, want 2", len(res.Orders))
	}
	// M1 result=yes, buy at 0.40 → won, PnL = 10 * (1 - 0.40) = 6
	// M2 result=no, buy at 0.55 → lost, PnL = -10 * 0.55 = -5.5
	var totalPnL float64
	for _, o := range res.Orders {
		totalPnL += o.PnL
	}
	wantPnL := 10*(1.0-0.40) - 10*0.55 // 6.0 - 5.5 = 0.5
	if totalPnL != wantPnL {
		t.Errorf("total PnL = %.4f, want %.4f", totalPnL, wantPnL)
	}
	if res.Summary.TotalSignals != 2 {
		t.Errorf("Summary.TotalSignals = %d, want 2", res.Summary.TotalSignals)
	}
	if res.Summary.Wins != 1 || res.Summary.Losses != 1 {
		t.Errorf("Summary Wins=%d Losses=%d, want 1/1", res.Summary.Wins, res.Summary.Losses)
	}
}

func TestRunStrategyUnknownStrategy(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	_, err := e.RunStrategy("nonexistent", 0)
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

func TestRunStrategyMinPriceFilter(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	e.factories = map[string]StrategyFactory{
		"stub": newStubStrategy,
	}
	// M1 ticks: 0.40, 0.45. M2 ticks: 0.55.
	// minPrice=0.50 filters out M1 orders (first tick 0.40 < 0.50).
	res, err := e.RunStrategy("stub", 0.50)
	if err != nil {
		t.Fatalf("RunStrategy: %v", err)
	}
	for _, o := range res.Orders {
		if o.Price < 0.50 {
			t.Errorf("order price %.2f < minPrice 0.50", o.Price)
		}
	}
}

func TestRunAllParallel(t *testing.T) {
	e := testEngine(t, seedFinalizedMatch)
	e.factories = map[string]StrategyFactory{
		"stub-a": newStubStrategy,
		"stub-b": newStubStrategy,
	}
	results, err := e.RunAll(0)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, res := range results {
		if len(res.Orders) != 2 {
			t.Errorf("strategy %s: orders = %d, want 2", res.Name, len(res.Orders))
		}
	}
}

func TestAvailableStrategiesSorted(t *testing.T) {
	e := testEngine(t, nil)
	e.factories = map[string]StrategyFactory{
		"zebra":  newStubStrategy,
		"alpha":  newStubStrategy,
		"middle": newStubStrategy,
	}
	names := e.AvailableStrategies()
	if len(names) != 3 {
		t.Fatalf("got %d names, want 3", len(names))
	}
	if names[0] != "alpha" || names[1] != "middle" || names[2] != "zebra" {
		t.Errorf("names = %v, want [alpha middle zebra]", names)
	}
}
