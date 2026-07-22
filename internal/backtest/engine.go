// Package backtest provides a reusable engine for replaying historical
// tick and point data through trading strategies. Replay-only — dashboard
// live queries live in internal/dashboarddata.
package backtest

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// MarketRow maps a market row from the DB.
type MarketRow struct {
	MarketTicker string
	PlayerName   string
	Result       string
	Status       string
}

// TickPrice maps a tick price row from the DB.
type TickPrice struct {
	TS    int64
	Price float64
}

// TickVolume is a timestamped dollar_volume sample for backtest replay.
type TickVolume struct {
	TS           int64
	DollarVolume float64
}

// Order is a resolved order with P&L.
type Order struct {
	Match     string  `json:"match"`
	Market    string  `json:"market"`
	Context   string  `json:"context"`
	SetNum    int     `json:"set_num"`
	Price     float64 `json:"price"`
	EdgeCents int     `json:"edge_cents"`
	Size      float64 `json:"size"`
	Won       bool    `json:"won"`
	PnL       float64 `json:"pnl"`
	Result    string  `json:"result"`
	TS        int64   `json:"ts"`
	Side      string  `json:"side,omitempty"` // "open" (buy) or "close" (sell)
}

// StrategyResult holds the output of running one strategy.
type StrategyResult struct {
	Name       string  `json:"name"`
	Orders     []Order `json:"orders"`
	MatchCount int     `json:"match_count"`
	Summary    Summary `json:"summary"`
}

// Summary holds aggregate stats for a strategy.
type Summary struct {
	TotalSignals  int     `json:"total_signals"`
	Wins          int     `json:"wins"`
	Losses        int     `json:"losses"`
	WinRate       float64 `json:"win_rate"`
	TotalInvested float64 `json:"total_invested"`
	TotalPayout   float64 `json:"total_payout"`
	NetPnL        float64 `json:"net_pnl"`
	ROI           float64 `json:"roi"`
	AvgEdge       float64 `json:"avg_edge"`
	AvgSize       float64 `json:"avg_size"`
	AvgPrice      float64 `json:"avg_price"`
	Sharpe        float64 `json:"sharpe"`
	Sortino       float64 `json:"sortino"`
	MaxDrawdown   float64 `json:"max_drawdown"`
	ProfitFactor  float64 `json:"profit_factor"`
	StdDev        float64 `json:"std_dev"`
	DownsideDev   float64 `json:"downside_dev"`
}

// ReplayStrategy extends algorithms.Strategy with backtest-specific methods.
type ReplayStrategy interface {
	algorithms.Strategy
	SetReplayTime(ts time.Time)
	OnPriceAt(marketTicker string, price float64, ts time.Time)
}

// StrategyFactory creates a strategy instance for backtest.
type StrategyFactory func(emitter algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy

// CloseTimeStrategy is an optional interface for strategies needing close_ts.
type CloseTimeStrategy interface {
	RegisterCloseTime(eventTicker string, closeTs int64)
}

// SeriesSetter is implemented by strategies that need series_ticker.
type SeriesSetter interface {
	SetSeriesTicker(eventTicker, seriesTicker string)
}

// SurfaceSetter is implemented by strategies needing court surface.
type SurfaceSetter interface {
	SetSurface(eventTicker, surface string)
}

// VolumeSetter is implemented by strategies needing dollar_volume series.
type VolumeSetter interface {
	SetVolumeSeries(marketTicker string, vols []algorithms.TickVolume)
}

// Engine holds loaded DB data and runs strategies against it. Replay-only.
type Engine struct {
	db            *gorm.DB
	log           *slog.Logger
	markets       map[string][]MarketRow
	marketCloseTs map[string]int64
	tickPrices    map[string][]TickPrice
	tickVolumes   map[string][]TickVolume
	points        map[string][]store.Point
	eventTitles   map[string]string
	eventSeries   map[string]string
	eventSurface  map[string]string
	factories     map[string]StrategyFactory
}

// NewEngine creates a backtest engine over an existing gorm DB handle.
// Calls load() to populate in-memory maps. Caller owns the DB handle —
// call Close() to release it when done.
//
// For DI in tests: pass a sqlite gorm.DB with the relevant tables
// migrated. For production: use NewEngineFromDSN.
func NewEngine(log *slog.Logger, db *gorm.DB) (*Engine, error) {
	e := &Engine{
		db:            db,
		log:           log,
		markets:       make(map[string][]MarketRow),
		marketCloseTs: make(map[string]int64),
		tickPrices:    make(map[string][]TickPrice),
		tickVolumes:   make(map[string][]TickVolume),
		points:        make(map[string][]store.Point),
		eventTitles:   make(map[string]string),
		eventSeries:   make(map[string]string),
		eventSurface:  make(map[string]string),
		factories:     DefaultFactories(),
	}

	if err := e.load(); err != nil {
		return nil, err
	}
	return e, nil
}

// NewEngineFromDSN opens a Postgres DB at dsn and wraps NewEngine.
// Convenience for production callers that don't already hold a gorm handle.
func NewEngineFromDSN(log *slog.Logger, dsn string) (*Engine, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	e, err := NewEngine(log, db)
	if err != nil {
		sqlDB, _ := db.DB()
		sqlDB.Close()
		return nil, err
	}
	return e, nil
}

// Close closes the underlying DB connection.
func (e *Engine) Close() {
	sqlDB, _ := e.db.DB()
	sqlDB.Close()
}

// AvailableStrategies returns the names of registered strategies.
func (e *Engine) AvailableStrategies() []string {
	names := make([]string, 0, len(e.factories))
	for name := range e.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RunStrategy runs a single strategy and returns its results.
func (e *Engine) RunStrategy(name string, minPrice float64) (*StrategyResult, error) {
	factory, ok := e.factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown strategy %q", name)
	}

	var orders []Order
	both := 0

	// Tick replay path
	for matchTicker, mkts := range e.markets {
		if len(mkts) < 2 {
			continue
		}
		both++

		homeMkt, awayMkt := e.orderMarketsByTitle(matchTicker, mkts)

		collector := algorithms.NewOrderCollector()
		strat := factory(collector, e.log)
		strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})
		e.wireStrategyContext(strat, matchTicker, homeMkt, awayMkt)

		e.replayInterleaved(strat, matchTicker, homeMkt, awayMkt)

		orders = append(orders, e.resolveOrdersWithSells(collector.Orders(), mkts, minPrice)...)
	}

	// Close-time replay path
	closeOrders := e.runCloseTimeBacktest(factory, minPrice)
	orders = append(orders, closeOrders...)

	return &StrategyResult{
		Name:       name,
		Orders:     orders,
		MatchCount: both,
		Summary:    computeSummary(orders),
	}, nil
}

// RunAll runs all registered strategies and returns their results.
func (e *Engine) RunAll(minPrice float64) ([]*StrategyResult, error) {
	names := e.AvailableStrategies()
	results := make([]*StrategyResult, len(names))
	errs := make([]error, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(idx int, strategyName string) {
			defer wg.Done()
			res, err := e.RunStrategy(strategyName, minPrice)
			results[idx] = res
			errs[idx] = err
		}(i, name)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
