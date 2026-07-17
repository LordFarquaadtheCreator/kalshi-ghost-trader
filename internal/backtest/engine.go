// Package backtest provides a reusable engine for replaying historical
// tick and point data through trading strategies. Extracted from cmd/backtest
// so both the CLI and the strategy API server share the same logic.
package backtest

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// PointRow maps a point-by-point score row from the DB.
type PointRow struct {
	TS         int64
	SetNum     int
	GameNum    int
	PointNum   int
	Server     int
	Scorer     int
	HomePts    string
	AwayPts    string
	HomeGames  int
	AwayGames  int
	IsTiebreak bool
}

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

// Engine holds loaded DB data and runs strategies against it.
type Engine struct {
	db            *sql.DB
	log           *slog.Logger
	markets       map[string][]MarketRow
	marketCloseTs map[string]int64
	tickPrices    map[string][]TickPrice
	pointsByMatch map[string][]PointRow
	eventTitles   map[string]string
	factories     map[string]StrategyFactory
}

// NewEngine creates a backtest engine from a read-only SQLite DB.
func NewEngine(dbPath string, log *slog.Logger) (*Engine, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	e := &Engine{
		db:            db,
		log:           log,
		markets:       make(map[string][]MarketRow),
		marketCloseTs: make(map[string]int64),
		tickPrices:    make(map[string][]TickPrice),
		pointsByMatch: make(map[string][]PointRow),
		eventTitles:   make(map[string]string),
		factories:     DefaultFactories(),
	}

	if err := e.load(); err != nil {
		db.Close()
		return nil, err
	}
	return e, nil
}

// Close closes the underlying DB connection.
func (e *Engine) Close() {
	e.db.Close()
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

func (e *Engine) load() error {
	ctx := context.Background()

	// Load event titles
	rows, err := e.db.QueryContext(ctx, `SELECT event_ticker, title FROM events`)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	for rows.Next() {
		var et, title string
		if err := rows.Scan(&et, &title); err != nil {
			rows.Close()
			return err
		}
		e.eventTitles[et] = title
	}
	rows.Close()

	// Load markets
	mRows, err := e.db.QueryContext(ctx, `SELECT event_ticker, market_ticker, player_name, result, status, close_ts FROM markets ORDER BY event_ticker, market_ticker`)
	if err != nil {
		return fmt.Errorf("query markets: %w", err)
	}
	for mRows.Next() {
		var et, mt, pn, res, st string
		var closeTs sql.NullInt64
		if err := mRows.Scan(&et, &mt, &pn, &res, &st, &closeTs); err != nil {
			mRows.Close()
			return err
		}
		e.markets[et] = append(e.markets[et], MarketRow{mt, pn, res, st})
		if closeTs.Valid && closeTs.Int64 > 0 {
			e.marketCloseTs[et] = closeTs.Int64
		}
	}
	mRows.Close()

	// Load tick prices
	tRows, err := e.db.QueryContext(ctx, `SELECT market_ticker, ts, price FROM ticks WHERE price IS NOT NULL AND price > 0 ORDER BY market_ticker, ts`)
	if err != nil {
		return fmt.Errorf("query ticks: %w", err)
	}
	for tRows.Next() {
		var mt string
		var ts int64
		var price float64
		if err := tRows.Scan(&mt, &ts, &price); err != nil {
			tRows.Close()
			return err
		}
		e.tickPrices[mt] = append(e.tickPrices[mt], TickPrice{ts, price})
	}
	tRows.Close()

	// Load points
	pRows, err := e.db.QueryContext(ctx, `
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points, home_games, away_games,
		       is_tiebreak
		FROM points WHERE ts_ms IS NOT NULL
		ORDER BY match_ticker, ts_ms`)
	if err != nil {
		return fmt.Errorf("query points: %w", err)
	}
	for pRows.Next() {
		var mt string
		var ts int64
		var setNum, gameNum, pointNum, server, scorer, homeGames, awayGames int
		var homePts, awayPts string
		var isTB int
		if err := pRows.Scan(&mt, &ts, &setNum, &gameNum, &pointNum, &server, &scorer, &homePts, &awayPts, &homeGames, &awayGames, &isTB); err != nil {
			pRows.Close()
			return err
		}
		e.pointsByMatch[mt] = append(e.pointsByMatch[mt], PointRow{
			TS: ts, SetNum: setNum, GameNum: gameNum, PointNum: pointNum,
			Server: server, Scorer: scorer, HomePts: homePts, AwayPts: awayPts,
			HomeGames: homeGames, AwayGames: awayGames, IsTiebreak: isTB == 1,
		})
	}
	pRows.Close()

	return nil
}

// RunStrategy runs a single strategy and returns its results.
func (e *Engine) RunStrategy(name string, minPrice float64) (*StrategyResult, error) {
	factory, ok := e.factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown strategy %q", name)
	}

	var orders []Order
	both := 0

	// Point-based replay path
	for matchTicker, pts := range e.pointsByMatch {
		mkts, ok := e.markets[matchTicker]
		if !ok || len(mkts) < 2 {
			continue
		}
		both++

		homeMkt, awayMkt := e.orderMarketsByTitle(matchTicker, mkts)

		collector := algorithms.NewOrderCollector()
		strat := factory(collector, e.log)
		strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})

		sort.Slice(pts, func(i, j int) bool { return pts[i].TS < pts[j].TS })

		tickIdx := map[string]int{homeMkt: 0, awayMkt: 0}
		for _, p := range pts {
			ptTime := time.UnixMilli(p.TS)
			strat.SetReplayTime(ptTime)
			for _, mkt := range []string{homeMkt, awayMkt} {
				ticks := e.tickPrices[mkt]
				for tickIdx[mkt] < len(ticks) && ticks[tickIdx[mkt]].TS <= p.TS {
					strat.OnPriceAt(mkt, ticks[tickIdx[mkt]].Price, time.UnixMilli(ticks[tickIdx[mkt]].TS))
					tickIdx[mkt]++
				}
			}
			strat.OnPoints([]store.Point{{
				MatchTicker: matchTicker,
				SetNumber:   p.SetNum,
				GameNumber:  p.GameNum,
				PointNumber: p.PointNum,
				Server:      p.Server,
				Scorer:      p.Scorer,
				HomePoints:  p.HomePts,
				AwayPoints:  p.AwayPts,
				HomeGames:   p.HomeGames,
				AwayGames:   p.AwayGames,
				IsTiebreak:  p.IsTiebreak,
				TsMs:        p.TS,
			}})
		}

		orders = append(orders, e.resolveOrders(collector.Orders(), mkts, minPrice)...)
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
	results := make([]*StrategyResult, 0, len(names))
	for _, name := range names {
		res, err := e.RunStrategy(name, minPrice)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (e *Engine) runCloseTimeBacktest(factory StrategyFactory, minPrice float64) []Order {
	collector := algorithms.NewOrderCollector()
	strat := factory(collector, e.log)

	cts, ok := strat.(CloseTimeStrategy)
	if !ok {
		return nil
	}

	var orders []Order
	for matchTicker, mkts := range e.markets {
		closeTs, ok := e.marketCloseTs[matchTicker]
		if !ok || closeTs == 0 {
			continue
		}
		if len(mkts) < 2 {
			continue
		}
		finalized := false
		for _, m := range mkts {
			if m.Status == "finalized" {
				finalized = true
				break
			}
		}
		if !finalized {
			continue
		}

		homeMkt, awayMkt := e.orderMarketsByTitle(matchTicker, mkts)
		cts.RegisterCloseTime(matchTicker, closeTs)
		strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})

		for _, mkt := range []string{homeMkt, awayMkt} {
			ticks := e.tickPrices[mkt]
			for _, t := range ticks {
				strat.OnPriceAt(mkt, t.Price, time.UnixMilli(t.TS))
			}
		}
		strat.UnregisterMarkets(matchTicker)
	}

	orders = e.resolveOrders(collector.Orders(), nil, minPrice)
	// resolveOrders needs market lookup for result; handle separately
	for i := range orders {
		mkts := e.markets[orders[i].Match]
		mktResult := ""
		for _, m := range mkts {
			if m.MarketTicker == orders[i].Market {
				mktResult = m.Result
				break
			}
		}
		orders[i].Result = mktResult
		orders[i].Won = mktResult == "yes"
		if orders[i].Won {
			orders[i].PnL = orders[i].Size * (1.0 - orders[i].Price)
		} else {
			orders[i].PnL = -orders[i].Size * orders[i].Price
		}
	}

	return orders
}

func (e *Engine) resolveOrders(raw []store.Order, mkts []MarketRow, minPrice float64) []Order {
	var orders []Order
	for _, o := range raw {
		if minPrice > 0 && o.MarketPrice < minPrice {
			continue
		}
		mktResult := ""
		if mkts != nil {
			for _, m := range mkts {
				if m.MarketTicker == o.MarketTicker {
					mktResult = m.Result
					break
				}
			}
		} else {
			for _, m := range e.markets[o.MatchTicker] {
				if m.MarketTicker == o.MarketTicker {
					mktResult = m.Result
					break
				}
			}
		}
		won := mktResult == "yes"
		var pnl float64
		if won {
			pnl = o.SuggestedSize * (1.0 - o.MarketPrice)
		} else {
			pnl = -o.SuggestedSize * o.MarketPrice
		}
		orders = append(orders, Order{
			Match: o.MatchTicker, Market: o.MarketTicker, Context: o.Context,
			SetNum: o.SetNumber, Price: o.MarketPrice, EdgeCents: o.EdgeCents,
			Size: o.SuggestedSize, Won: won, PnL: pnl, Result: mktResult,
		})
	}
	return orders
}

func (e *Engine) orderMarketsByTitle(eventTicker string, mkts []MarketRow) (home, away string) {
	if len(mkts) < 2 {
		return mkts[0].MarketTicker, ""
	}
	title, ok := e.eventTitles[eventTicker]
	if !ok {
		return mkts[0].MarketTicker, mkts[1].MarketTicker
	}
	parts := strings.SplitN(title, " vs ", 2)
	if len(parts) != 2 {
		return mkts[0].MarketTicker, mkts[1].MarketTicker
	}
	homeLN := lastName(strings.TrimSpace(parts[0]))
	for _, m := range mkts {
		if lastName(m.PlayerName) == homeLN {
			return m.MarketTicker, otherMarket(mkts, m.MarketTicker)
		}
	}
	return mkts[0].MarketTicker, mkts[1].MarketTicker
}

func lastName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parts[len(parts)-1], "."))
}

func otherMarket(mkts []MarketRow, skip string) string {
	for _, m := range mkts {
		if m.MarketTicker != skip {
			return m.MarketTicker
		}
	}
	return ""
}

func computeSummary(orders []Order) Summary {
	s := Summary{TotalSignals: len(orders)}
	if len(orders) == 0 {
		return s
	}

	for _, o := range orders {
		if o.Won {
			s.Wins++
			s.TotalPayout += o.Size
		} else {
			s.Losses++
		}
		s.NetPnL += o.PnL
		s.TotalInvested += o.Size * o.Price
		s.AvgEdge += float64(o.EdgeCents)
		s.AvgSize += o.Size
		s.AvgPrice += o.Price
	}

	n := float64(len(orders))
	s.WinRate = float64(s.Wins) / n * 100
	if s.TotalInvested > 0 {
		s.ROI = s.NetPnL / s.TotalInvested * 100
	}
	s.AvgEdge /= n
	s.AvgSize /= n
	s.AvgPrice /= n

	// Risk-adjusted metrics
	var sumSqDev, sumDownside, grossWin, grossLoss float64
	var cumulative, peak, maxDD float64
	for _, o := range orders {
		dev := o.PnL - (s.NetPnL / n)
		sumSqDev += dev * dev
		if o.PnL < 0 {
			sumDownside += o.PnL * o.PnL
			grossLoss += -o.PnL
		} else {
			grossWin += o.PnL
		}
		cumulative += o.PnL
		if cumulative > peak {
			peak = cumulative
		}
		dd := peak - cumulative
		if dd > maxDD {
			maxDD = dd
		}
	}
	s.StdDev = sqrt(sumSqDev / n)
	s.DownsideDev = sqrt(sumDownside / n)
	s.MaxDrawdown = maxDD
	if s.StdDev > 0 {
		s.Sharpe = (s.NetPnL / n) / s.StdDev
	}
	if s.DownsideDev > 0 {
		s.Sortino = (s.NetPnL / n) / s.DownsideDev
	}
	if grossLoss > 0 {
		s.ProfitFactor = grossWin / grossLoss
	}

	return s
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Sqrt(x)
}
