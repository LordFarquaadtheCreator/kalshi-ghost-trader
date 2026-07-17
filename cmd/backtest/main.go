// Command backtest replays historical point + tick data from the SQLite DB
// through a trading strategy and reports P&L.
//
// Usage:
//
//	go run ./cmd/backtest -strategy <name> [flags]
//
// Default db_path is kalshi_tennis.db in the current directory.
// Available strategies: matchpoint, matchpoint-aggro, setpoint, setpoint-serve,
// setpoint-cheap, fadelongshot
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// replayStrategy extends algorithms.Strategy with backtest-specific
// time-replay methods. Strategies must implement this to be backtestable.
type replayStrategy interface {
	algorithms.Strategy
	SetReplayTime(ts time.Time)
	OnPriceAt(marketTicker string, price float64, ts time.Time)
}

// strategyFactory creates a new strategy instance for backtest.
type strategyFactory func(emitter algorithms.OrderEmitter, log *slog.Logger) replayStrategy

// closeTimeStrategy is an optional interface for strategies that need
// close_ts (e.g. fade-longshot). The backtest engine calls RegisterCloseTime
// if the strategy implements this.
type closeTimeStrategy interface {
	RegisterCloseTime(eventTicker string, closeTs int64)
}

// scoreObserver is an optional interface for strategies that want
// point-by-point score updates during backtest replay.
type scoreObserver interface {
	OnPoint(eventTicker string, p store.Point)
}

var strategies = map[string]strategyFactory{
	"matchpoint": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewMatchPointStrategy(em, log)
	},
	"matchpoint-aggro": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewSetPointStrategy(em, log, algorithms.SetPointConfig{
			IncludeSetPoints: false,
			IncludeReturning: true,
			ServeConvProb:    0.97,
			ReturnConvProb:   0.89,
			MinMarketPrice:   0.05,
			MinEdgeCents:     1,
			Label:            "matchpoint-aggro",
		})
	},
	"setpoint": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewSetPointStrategy(em, log, algorithms.DefaultSetPointConfig())
	},
	"setpoint-serve": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.IncludeReturning = false
		cfg.Label = "setpoint-serve"
		return algorithms.NewSetPointStrategy(em, log, cfg)
	},
	"setpoint-cheap": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.MaxMarketPrice = 0.50
		cfg.Label = "setpoint-cheap"
		return algorithms.NewSetPointStrategy(em, log, cfg)
	},
	"fadelongshot": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewFadeLongshotStrategy(em, log, algorithms.DefaultFadeLongshotConfig())
	},
	"breakpoint": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewBreakPointStrategy(em, log, algorithms.DefaultBreakPointConfig())
	},
	"convexpool": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewConvexPoolStrategy(em, log, algorithms.DefaultConvexPoolConfig())
	},
	"breakback": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewBreakBackStrategy(em, log, algorithms.DefaultBreakBackConfig())
	},
	"nofade": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewNoFadeStrategy(em, log, algorithms.DefaultNoFadeConfig())
	},
	"server1530": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewServer1530Strategy(em, log, algorithms.DefaultServer1530Config())
	},
	"setdown": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewSetDownStrategy(em, log, algorithms.DefaultSetDownConfig())
	},
	"tiebreak": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewTiebreakStrategy(em, log, algorithms.DefaultTiebreakConfig())
	},
	"comeback040": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewComeback040Strategy(em, log, algorithms.DefaultComeback040Config())
	},
	"calibrated-markov": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewCalibratedMarkovStrategy(em, log, algorithms.DefaultCalibratedMarkovConfig())
	},
	"cross-arb": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewCrossArbStrategy(em, log, algorithms.DefaultCrossArbConfig())
	},
	"tiebreak-server": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewTiebreakServerStrategy(em, log, algorithms.DefaultTiebreakServerConfig())
	},
	"set1winner": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewSet1WinnerStrategy(em, log, algorithms.DefaultSet1WinnerConfig())
	},
	"volratio": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewVolumeRatioStrategy(em, log, algorithms.DefaultVolumeRatioConfig())
	},
	"surface-markov": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewSurfaceMarkovStrategy(em, log, algorithms.DefaultSurfaceMarkovConfig())
	},
	"spike-fade": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		return algorithms.NewSpikeFadeStrategy(em, log, algorithms.DefaultSpikeFadeConfig())
	},
	"fadelongshot-itf": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultFadeLongshotConfig()
		cfg.Label = "fadelongshot-itf"
		cfg.SeriesFilter = []string{"KXITFMATCH", "KXITFWMATCH", "KXITFDOUBLES", "KXITFWDOUBLES"}
		return algorithms.NewFadeLongshotStrategy(em, log, cfg)
	},
	"fadelongshot-challenger": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultFadeLongshotConfig()
		cfg.Label = "fadelongshot-challenger"
		cfg.SeriesFilter = []string{"KXATPCHALLENGERMATCH", "KXWTACHALLENGERMATCH"}
		return algorithms.NewFadeLongshotStrategy(em, log, cfg)
	},
	"fadelongshot-atp": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultFadeLongshotConfig()
		cfg.Label = "fadelongshot-atp"
		cfg.SeriesFilter = []string{"KXATPMATCH", "KXATPDOUBLES"}
		return algorithms.NewFadeLongshotStrategy(em, log, cfg)
	},
	"fadelongshot-wta": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultFadeLongshotConfig()
		cfg.Label = "fadelongshot-wta"
		cfg.SeriesFilter = []string{"KXWTAMATCH", "KXWTADOUBLES"}
		return algorithms.NewFadeLongshotStrategy(em, log, cfg)
	},
	"fadelongshot-doubles": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultFadeLongshotConfig()
		cfg.Label = "fadelongshot-doubles"
		cfg.SeriesFilter = []string{"KXATPDOUBLES", "KXWTADOUBLES", "KXITFDOUBLES", "KXITFWDOUBLES"}
		return algorithms.NewFadeLongshotStrategy(em, log, cfg)
	},
	"fadelongshot-evening": func(em algorithms.OrderEmitter, log *slog.Logger) replayStrategy {
		cfg := algorithms.DefaultFadeLongshotConfig()
		cfg.Label = "fadelongshot-evening"
		cfg.UTCHourStart = 18
		cfg.UTCHourEnd = 4
		return algorithms.NewFadeLongshotStrategy(em, log, cfg)
	},
}

type marketRow struct {
	marketTicker string
	playerName   string
	result       string
	status       string
}

// eventTitles maps event_ticker -> title ("Home vs Away").
var eventTitles = make(map[string]string)

type tickPrice struct {
	ts    int64
	price float64
}

type order struct {
	match     string
	market    string
	context   string
	setNum    int
	price     float64
	edgeCents int
	size      float64
	won       bool
	pnl       float64
	result    string
}

func main() {
	dbPath := flag.String("db", "kalshi_tennis.db", "path to SQLite DB")
	strategyName := flag.String("strategy", "all", "strategy to backtest (use 'all' for all strategies)")
	minPrice := flag.Float64("min-price", 0.0, "skip signals below this market price (0=disabled)")
	maxPrice := flag.Float64("max-price", 0.0, "skip signals above this market price (0=disabled)")
	debugMode := flag.Bool("debug", false, "enable debug logging to see strategy filter reasons")
	flag.Parse()

	var selected []string
	if *strategyName == "all" {
		for name := range strategies {
			selected = append(selected, name)
		}
		sort.Strings(selected)
	} else {
		if _, ok := strategies[*strategyName]; !ok {
			available := make([]string, 0, len(strategies)+1)
			available = append(available, "all")
			for name := range strategies {
				available = append(available, name)
			}
			sort.Strings(available)
			fmt.Fprintf(os.Stderr, "unknown strategy %q (available: %s)\n", *strategyName, strings.Join(available, ", "))
			os.Exit(1)
		}
		selected = []string{*strategyName}
	}

	logLevel := slog.LevelWarn
	if *debugMode {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	ctx := context.Background()

	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)", *dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Load event titles for home/away market ordering
	// eventSeries maps event_ticker -> series_ticker for ML strategies
	eventSeries := make(map[string]string)
	eventSurface := make(map[string]string)
	eventRows, err := db.QueryContext(ctx, `SELECT event_ticker, title, series_ticker FROM events`)
	if err != nil {
		log.Error("query events", "err", err)
		os.Exit(1)
	}
	for eventRows.Next() {
		var et, title, series string
		if err := eventRows.Scan(&et, &title, &series); err != nil {
			log.Error("scan event", "err", err)
			os.Exit(1)
		}
		eventTitles[et] = title
		eventSeries[et] = series
	}
	eventRows.Close()

	// Load surface from flashscore_matches
	fsRows, err := db.QueryContext(ctx,
		`SELECT event_ticker, surface FROM flashscore_matches WHERE event_ticker IS NOT NULL AND surface IS NOT NULL`)
	if err != nil {
		log.Error("query flashscore surfaces", "err", err)
		os.Exit(1)
	}
	for fsRows.Next() {
		var et, surface string
		if err := fsRows.Scan(&et, &surface); err != nil {
			log.Error("scan surface", "err", err)
			os.Exit(1)
		}
		eventSurface[et] = surface
	}
	fsRows.Close()

	// Load markets
	fmt.Println("Loading markets...")
	markets := make(map[string][]marketRow)
	marketCloseTs := make(map[string]int64)
	marketRows, err := db.QueryContext(ctx, `SELECT event_ticker, market_ticker, player_name, result, status, close_ts FROM markets ORDER BY event_ticker, market_ticker`)
	if err != nil {
		log.Error("query markets", "err", err)
		os.Exit(1)
	}
	for marketRows.Next() {
		var et, mt, pn, res, st string
		var closeTs sql.NullInt64
		if err := marketRows.Scan(&et, &mt, &pn, &res, &st, &closeTs); err != nil {
			log.Error("scan market", "err", err)
			os.Exit(1)
		}
		markets[et] = append(markets[et], marketRow{mt, pn, res, st})
		if closeTs.Valid && closeTs.Int64 > 0 {
			marketCloseTs[et] = closeTs.Int64
		}
	}
	marketRows.Close()

	// Load tick prices per market
	fmt.Println("Loading tick prices...")
	tickPrices := make(map[string][]tickPrice)
	tickRows, err := db.QueryContext(ctx, `SELECT market_ticker, ts, price FROM ticks WHERE price IS NOT NULL AND price > 0 ORDER BY market_ticker, ts`)
	if err != nil {
		log.Error("query ticks", "err", err)
		os.Exit(1)
	}
	count := 0
	for tickRows.Next() {
		var mt string
		var ts int64
		var price float64
		if err := tickRows.Scan(&mt, &ts, &price); err != nil {
			log.Error("scan tick", "err", err)
			os.Exit(1)
		}
		tickPrices[mt] = append(tickPrices[mt], tickPrice{ts, price})
		count++
	}
	tickRows.Close()
	fmt.Printf("Loaded %d tick prices across %d markets\n", count, len(tickPrices))

	// Load dollar_volume time series per market (for volratio strategy)
	fmt.Println("Loading tick volumes...")
	tickVolumes := make(map[string][]algorithms.TickVolume)
	vRows, err := db.QueryContext(ctx,
		`SELECT market_ticker, ts, dollar_volume FROM ticks
		 WHERE dollar_volume IS NOT NULL AND dollar_volume > 0
		 ORDER BY market_ticker, ts`)
	if err != nil {
		log.Error("query tick volumes", "err", err)
		os.Exit(1)
	}
	volCount := 0
	for vRows.Next() {
		var mt string
		var ts int64
		var dv float64
		if err := vRows.Scan(&mt, &ts, &dv); err != nil {
			log.Error("scan volume", "err", err)
			os.Exit(1)
		}
		tickVolumes[mt] = append(tickVolumes[mt], algorithms.TickVolume{TS: ts, DollarVolume: dv})
		volCount++
	}
	vRows.Close()
	fmt.Printf("Loaded %d volume samples across %d markets\n", volCount, len(tickVolumes))

	// Load point-by-point score data
	fmt.Println("Loading points...")
	points := make(map[string][]store.Point)
	pRows, err := db.QueryContext(ctx, `
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0), COALESCE(away_set_games, 0),
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM points WHERE ts_ms IS NOT NULL
		ORDER BY match_ticker, ts_ms
	`)
	if err != nil {
		log.Error("query points", "err", err)
		os.Exit(1)
	}
	pointCount := 0
	for pRows.Next() {
		var mt string
		var p store.Point
		var isTB, isBP, isSP, isMP int
		if err := pRows.Scan(
			&mt, &p.TS, &p.SetNumber, &p.GameNumber, &p.PointNumber,
			&p.Server, &p.Scorer, &p.HomePoints, &p.AwayPoints,
			&p.HomeGames, &p.AwayGames,
			&p.HomeSetGames, &p.AwaySetGames,
			&isTB, &isBP, &isSP, &isMP,
		); err != nil {
			pRows.Close()
			log.Error("scan point", "err", err)
			os.Exit(1)
		}
		p.IsTiebreak = isTB != 0
		p.IsBreakPoint = isBP != 0
		p.IsSetPoint = isSP != 0
		p.IsMatchPoint = isMP != 0
		points[mt] = append(points[mt], p)
		pointCount++
	}
	pRows.Close()
	fmt.Printf("Loaded %d points across %d matches\n", pointCount, len(points))

	fmt.Printf("Matches with markets: %d\n", len(markets))

	for _, name := range selected {
		fmt.Printf("\n%s\n", strings.Repeat("=", 80))
		fmt.Printf("STRATEGY: %s\n", name)
		fmt.Printf("%s\n", strings.Repeat("=", 80))
		runStrategy(name, strategies[name], log, markets, marketCloseTs, tickPrices, tickVolumes, points, eventSeries, eventSurface, *minPrice, *maxPrice)
	}

	// Aggregate summary across all strategies
	if len(selected) > 1 {
		printAggregate(filterOrders(allOrders, *minPrice, *maxPrice))
	}
}

// allOrders collects orders across all strategies for aggregate reporting.
var allOrders []order

// filterOrders applies client-side price filters for display.
// minPrice/maxPrice = 0 means no filter on that bound.
func filterOrders(orders []order, minPrice, maxPrice float64) []order {
	if minPrice <= 0 && maxPrice <= 0 {
		return orders
	}
	filtered := orders[:0:0]
	for _, o := range orders {
		if minPrice > 0 && o.price < minPrice {
			continue
		}
		if maxPrice > 0 && o.price > maxPrice {
			continue
		}
		filtered = append(filtered, o)
	}
	return filtered
}

// seriesSetter is implemented by strategies needing series_ticker.
type seriesSetter interface {
	SetSeriesTicker(eventTicker, seriesTicker string)
}

// surfaceSetter is implemented by strategies needing court surface.
type surfaceSetter interface {
	SetSurface(eventTicker, surface string)
}

// volumeSetter is implemented by strategies needing dollar_volume series.
type volumeSetter interface {
	SetVolumeSeries(marketTicker string, vols []algorithms.TickVolume)
}

// wireStrategyContext sets series, surface, and volume data on strategies
// that implement the corresponding setter interfaces.
func wireStrategyContext(
	strat replayStrategy,
	matchTicker, homeMkt, awayMkt string,
	eventSeries, eventSurface map[string]string,
	tickVolumes map[string][]algorithms.TickVolume,
) {
	if ss, ok := strat.(seriesSetter); ok {
		if series := eventSeries[matchTicker]; series != "" {
			ss.SetSeriesTicker(matchTicker, series)
		}
	}
	if ss, ok := strat.(surfaceSetter); ok {
		if surface := eventSurface[matchTicker]; surface != "" {
			ss.SetSurface(matchTicker, surface)
		}
	}
	if vs, ok := strat.(volumeSetter); ok {
		if vols := tickVolumes[homeMkt]; len(vols) > 0 {
			vs.SetVolumeSeries(homeMkt, vols)
		}
		if vols := tickVolumes[awayMkt]; len(vols) > 0 {
			vs.SetVolumeSeries(awayMkt, vols)
		}
	}
}

// runStrategy replays historical data through a single strategy and prints results.
func runStrategy(
	name string,
	factory strategyFactory,
	log *slog.Logger,
	markets map[string][]marketRow,
	marketCloseTs map[string]int64,
	tickPrices map[string][]tickPrice,
	tickVolumes map[string][]algorithms.TickVolume,
	points map[string][]store.Point,
	eventSeries, eventSurface map[string]string,
	minPrice, maxPrice float64,
) {
	var orders []order
	both := 0
	for matchTicker, mkts := range markets {
		if len(mkts) < 2 {
			continue
		}
		both++

		homeMkt, awayMkt := orderMarketsByTitle(matchTicker, mkts)

		collector := algorithms.NewOrderCollector()
		strat := factory(collector, log)
		strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})
		wireStrategyContext(strat, matchTicker, homeMkt, awayMkt, eventSeries, eventSurface, tickVolumes)

		replayInterleaved(strat, matchTicker, homeMkt, awayMkt, tickPrices, points)

		for _, o := range collector.Orders() {
			mktResult := ""
			for _, m := range mkts {
				if m.marketTicker == o.MarketTicker {
					mktResult = m.result
					break
				}
			}
			// Skip unresolved markets — can't evaluate PnL
			if mktResult == "" {
				continue
			}
			won := mktResult == "yes"
			// buy_no: win when result is "no". PnL = size*(1-no_price) on win, -size*no_price on loss.
			// no_price stored in MarketPrice for buy_no orders.
			if o.Action == "buy_no" {
				won = mktResult == "no"
			}
			var pnl float64
			if won {
				pnl = o.SuggestedSize * (1.0 - o.MarketPrice)
			} else {
				pnl = -o.SuggestedSize * o.MarketPrice
			}
			ord := order{
				match: o.MatchTicker, market: o.MarketTicker, context: o.Context,
				setNum: o.SetNumber,
				price:  o.MarketPrice, edgeCents: o.EdgeCents, size: o.SuggestedSize,
				won: won, pnl: pnl, result: mktResult,
			}
			orders = append(orders, ord)
			allOrders = append(allOrders, ord)
		}
	}

	// Close-time backtest path
	closeOrders := runCloseTimeBacktest(factory, log, markets, marketCloseTs, tickPrices, tickVolumes, eventTitles, eventSeries, eventSurface)
	orders = append(orders, closeOrders...)
	allOrders = append(allOrders, closeOrders...)

	// Filter at display layer
	filtered := filterOrders(orders, minPrice, maxPrice)
	printSummary(name, filtered, both)
}

// printSummary prints backtest results for a single strategy.
func printSummary(name string, orders []order, both int) {
	fmt.Printf("Matches with markets: %d\n", both)
	fmt.Printf("Total signals: %d\n", len(orders))

	if len(orders) == 0 {
		fmt.Println("No orders would have been emitted.")
		return
	}

	wins, losses := 0, 0
	var totalPnL, totalInvested, totalPayout float64
	for _, o := range orders {
		if o.won {
			wins++
			totalPayout += o.size
		} else {
			losses++
		}
		totalPnL += o.pnl
		totalInvested += o.size * o.price
	}

	fmt.Printf("Wins: %d (%.1f%%)\n", wins, float64(wins)/float64(len(orders))*100)
	fmt.Printf("Losses: %d (%.1f%%)\n", losses, float64(losses)/float64(len(orders))*100)
	fmt.Printf("Total invested: $%.2f\n", totalInvested)
	fmt.Printf("Total payout: $%.2f\n", totalPayout)
	fmt.Printf("Net P&L: $%.2f\n", totalPnL)
	if totalInvested > 0 {
		fmt.Printf("ROI: %.1f%%\n", totalPnL/totalInvested*100)
	}

	var sumEdge, sumSize, sumPrice float64
	for _, o := range orders {
		sumEdge += float64(o.edgeCents)
		sumSize += o.size
		sumPrice += o.price
	}
	n := float64(len(orders))
	fmt.Printf("Avg edge: %.1f cents\n", sumEdge/n)
	fmt.Printf("Avg size: %.1f\n", sumSize/n)
	fmt.Printf("Avg price: %.3f\n", sumPrice/n)

	// Risk-adjusted metrics
	var sumSqDev, sumDownside, grossWin, grossLoss float64
	var cumulative, peak, maxDD float64
	for _, o := range orders {
		dev := o.pnl - (totalPnL / n)
		sumSqDev += dev * dev
		if o.pnl < 0 {
			sumDownside += o.pnl * o.pnl
			grossLoss += -o.pnl
		} else {
			grossWin += o.pnl
		}
		cumulative += o.pnl
		if cumulative > peak {
			peak = cumulative
		}
		dd := peak - cumulative
		if dd > maxDD {
			maxDD = dd
		}
	}
	stddev := math.Sqrt(sumSqDev / n)
	downsideDev := math.Sqrt(sumDownside / n)
	var sharpe, sortino, profitFactor float64
	if stddev > 0 {
		sharpe = (totalPnL / n) / stddev
	}
	if downsideDev > 0 {
		sortino = (totalPnL / n) / downsideDev
	}
	if grossLoss > 0 {
		profitFactor = grossWin / grossLoss
	}

	fmt.Printf("\n--- Risk-Adjusted Metrics ---\n")
	fmt.Printf("Sharpe (per-trade): %.4f\n", sharpe)
	fmt.Printf("Sortino (per-trade): %.4f\n", sortino)
	fmt.Printf("Max drawdown: $%.2f\n", maxDD)
	fmt.Printf("Profit factor: %.2f\n", profitFactor)
	fmt.Printf("Std dev per trade: $%.2f\n", stddev)
	fmt.Printf("Downside dev per trade: $%.2f\n", downsideDev)

	// Per-order detail
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].match != orders[j].match {
			return orders[i].match < orders[j].match
		}
		return orders[i].setNum < orders[j].setNum
	})

	fmt.Printf("\n%-45s %-20s %6s %5s %6s %4s %8s\n", "match", "ctx", "price", "edge", "size", "won", "pnl")
	fmt.Println(strings.Repeat("-", 100))
	for _, o := range orders {
		wonStr := "N"
		if o.won {
			wonStr = "Y"
		}
		fmt.Printf("%-45s %-20s %6.3f %5dc %6.1f %4s %8.2f\n",
			o.match, o.context, o.price, o.edgeCents, o.size, wonStr, o.pnl)
	}
}

// printAggregate prints a combined summary across all strategies.
func printAggregate(orders []order) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 80))
	fmt.Printf("AGGREGATE — ALL STRATEGIES\n")
	fmt.Printf("%s\n", strings.Repeat("=", 80))
	fmt.Printf("Total signals: %d\n", len(orders))

	if len(orders) == 0 {
		fmt.Println("No orders would have been emitted.")
		return
	}

	wins, losses := 0, 0
	var totalPnL, totalInvested, totalPayout float64
	for _, o := range orders {
		if o.won {
			wins++
			totalPayout += o.size
		} else {
			losses++
		}
		totalPnL += o.pnl
		totalInvested += o.size * o.price
	}

	fmt.Printf("Wins: %d (%.1f%%)\n", wins, float64(wins)/float64(len(orders))*100)
	fmt.Printf("Losses: %d (%.1f%%)\n", losses, float64(losses)/float64(len(orders))*100)
	fmt.Printf("Total invested: $%.2f\n", totalInvested)
	fmt.Printf("Total payout: $%.2f\n", totalPayout)
	fmt.Printf("Net P&L: $%.2f\n", totalPnL)
	if totalInvested > 0 {
		fmt.Printf("ROI: %.1f%%\n", totalPnL/totalInvested*100)
	}
}

// replayInterleaved feeds price ticks and score events to a strategy in
// timestamp order. Score events are only fed to strategies implementing scoreObserver.
func replayInterleaved(
	strat replayStrategy,
	matchTicker, homeMkt, awayMkt string,
	tickPrices map[string][]tickPrice,
	points map[string][]store.Point,
) {
	type event struct {
		ts    int64
		kind  int // 0=price, 1=score
		mkt   string
		price float64
		point store.Point
	}

	var events []event

	for _, mkt := range []string{homeMkt, awayMkt} {
		for _, t := range tickPrices[mkt] {
			events = append(events, event{ts: t.ts, kind: 0, mkt: mkt, price: t.price})
		}
	}

	for _, p := range points[matchTicker] {
		events = append(events, event{ts: p.TS, kind: 1, point: p})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })

	scoreObs, _ := strat.(scoreObserver)

	for _, ev := range events {
		ts := time.UnixMilli(ev.ts)
		strat.SetReplayTime(ts)
		if ev.kind == 0 {
			strat.OnPriceAt(ev.mkt, ev.price, ts)
		} else if scoreObs != nil {
			scoreObs.OnPoint(matchTicker, ev.point)
		}
	}
}

// orderMarketsByTitle determines [home, away] market order from the event title.
// Kalshi titles are "Home vs Away". Falls back to DB order if matching fails.
func orderMarketsByTitle(eventTicker string, mkts []marketRow) (home, away string) {
	if len(mkts) < 2 {
		return mkts[0].marketTicker, ""
	}
	title, ok := eventTitles[eventTicker]
	if !ok {
		return mkts[0].marketTicker, mkts[1].marketTicker
	}
	parts := strings.SplitN(title, " vs ", 2)
	if len(parts) != 2 {
		return mkts[0].marketTicker, mkts[1].marketTicker
	}
	homeLN := lastName(strings.TrimSpace(parts[0]))

	for _, m := range mkts {
		if lastName(m.playerName) == homeLN {
			return m.marketTicker, otherMarket(mkts, m.marketTicker)
		}
	}
	return mkts[0].marketTicker, mkts[1].marketTicker
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

func otherMarket(mkts []marketRow, skip string) string {
	for _, m := range mkts {
		if m.marketTicker != skip {
			return m.marketTicker
		}
	}
	return ""
}

// runCloseTimeBacktest replays tick data through strategies that use
// close_ts (e.g. fadelongshot). Iterates ALL finalized events with both
// markets and close_ts, not just those with points data.
func runCloseTimeBacktest(
	factory strategyFactory,
	log *slog.Logger,
	markets map[string][]marketRow,
	marketCloseTs map[string]int64,
	tickPrices map[string][]tickPrice,
	tickVolumes map[string][]algorithms.TickVolume,
	eventTitles map[string]string,
	eventSeries, eventSurface map[string]string,
) []order {
	collector := algorithms.NewOrderCollector()
	strat := factory(collector, log)

	cts, ok := strat.(closeTimeStrategy)
	if !ok {
		return nil
	}

	var orders []order
	both := 0
	for matchTicker, mkts := range markets {
		closeTs, ok := marketCloseTs[matchTicker]
		if !ok || closeTs == 0 {
			continue
		}
		if len(mkts) < 2 {
			continue
		}
		// Only finalized markets
		finalized := false
		for _, m := range mkts {
			if m.status == "finalized" {
				finalized = true
				break
			}
		}
		if !finalized {
			continue
		}
		both++

		homeMkt, awayMkt := orderMarketsByTitle(matchTicker, mkts)
		cts.RegisterCloseTime(matchTicker, closeTs)
		strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})
		wireStrategyContext(strat, matchTicker, homeMkt, awayMkt, eventSeries, eventSurface, tickVolumes)

		// Replay all ticks for both markets in chronological order
		for _, mkt := range []string{homeMkt, awayMkt} {
			ticks := tickPrices[mkt]
			for _, t := range ticks {
				strat.OnPriceAt(mkt, t.price, time.UnixMilli(t.ts))
			}
		}
		strat.UnregisterMarkets(matchTicker)
	}

	fmt.Printf("Close-time backtest: scanned %d finalized events with close_ts\n", both)

	for _, o := range collector.Orders() {
		mktResult := ""
		for _, m := range markets[o.MatchTicker] {
			if m.marketTicker == o.MarketTicker {
				mktResult = m.result
				break
			}
		}
		won := mktResult == "yes"
		var pnl float64
		if won {
			pnl = o.SuggestedSize * (1.0 - o.MarketPrice)
		} else {
			pnl = -o.SuggestedSize * o.MarketPrice
		}
		orders = append(orders, order{
			match: o.MatchTicker, market: o.MarketTicker, context: o.Context,
			setNum: o.SetNumber,
			price:  o.MarketPrice, edgeCents: o.EdgeCents, size: o.SuggestedSize,
			won: won, pnl: pnl, result: mktResult,
		})
	}

	return orders
}
