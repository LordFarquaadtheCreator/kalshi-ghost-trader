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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
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

// TickVolume is a timestamped dollar_volume sample for backtest replay.
type TickVolume struct {
	TS           int64
	DollarVolume float64
}

// NewEngine creates a backtest engine from a Postgres DB.
// Reads DSN from the global config.Cfg.
func NewEngine(log *slog.Logger) (*Engine, error) {
	db, err := gorm.Open(postgres.Open(config.Cfg.DBDSN), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

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

// EventTitle returns the cached title for an event, or empty string if unknown.
func (e *Engine) EventTitle(eventTicker string) string {
	return e.eventTitles[eventTicker]
}

// EventOccurrenceTS returns a map of event_ticker → occurrence_ts for the given events.
// Queries the DB live so data is fresh.
func (e *Engine) EventOccurrenceTS(ctx context.Context, eventTickers []string) (map[string]int64, error) {
	if len(eventTickers) == 0 {
		return nil, nil
	}
	var results []struct {
		EventTicker  string `gorm:"column:event_ticker"`
		OccurrenceTS int64  `gorm:"column:occurrence_ts"`
	}
	err := e.db.WithContext(ctx).Raw(
		`SELECT event_ticker, MAX(occurrence_ts) as occurrence_ts FROM markets WHERE event_ticker IN ? GROUP BY event_ticker`,
		eventTickers).Scan(&results).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(results))
	for _, r := range results {
		out[r.EventTicker] = r.OccurrenceTS
	}
	return out, nil
}

// LatestTickTS returns the most recent tick timestamp per event_ticker.
// Used by the dashboard to classify matches as live based on Kalshi tick
// activity, independent of API-Tennis score data.
func (e *Engine) LatestTickTS(ctx context.Context, eventTickers []string) (map[string]int64, error) {
	if len(eventTickers) == 0 {
		return nil, nil
	}
	var results []struct {
		EventTicker string `gorm:"column:event_ticker"`
		TS          int64  `gorm:"column:ts"`
	}
	err := e.db.WithContext(ctx).Raw(
		`SELECT m.event_ticker, MAX(t.ts) as ts
		 FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker
		 WHERE m.event_ticker IN ? GROUP BY m.event_ticker`,
		eventTickers).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("latest tick ts: %w", err)
	}
	out := make(map[string]int64, len(results))
	for _, r := range results {
		out[r.EventTicker] = r.TS
	}
	return out, nil
}

// LiveScore is the latest point-by-point score for a tracked match.
type LiveScore struct {
	EventTicker  string `json:"event_ticker"`
	SetNumber    int    `json:"set_number"`
	GameNumber   int    `json:"game_number"`
	PointNumber  int    `json:"point_number"`
	Server       int    `json:"server"`
	HomePoints   string `json:"home_points"`
	AwayPoints   string `json:"away_points"`
	HomeGames    int    `json:"home_games"`
	AwayGames    int    `json:"away_games"`
	HomeSetGames int    `json:"home_set_games"`
	AwaySetGames int    `json:"away_set_games"`
	IsTiebreak   bool   `json:"is_tiebreak"`
	IsBreakPoint bool   `json:"is_break_point"`
	IsSetPoint   bool   `json:"is_set_point"`
	IsMatchPoint bool   `json:"is_match_point"`
}

// LatestScores returns the most recent point for each given event ticker.
// API-Tennis (points table) is the primary source. For events with no
// API-Tennis data, Kalshi live-data scores (kalshi_scores table) fill the gap.
func (e *Engine) LatestScores(ctx context.Context, eventTickers []string) (map[string]*LiveScore, error) {
	if len(eventTickers) == 0 {
		return nil, nil
	}
	var apiScores []LiveScore
	err := e.db.WithContext(ctx).Raw(`
		SELECT match_ticker as event_ticker, set_number, game_number, point_number,
		       server, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0) as home_set_games, COALESCE(away_set_games, 0) as away_set_games,
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY match_ticker
				ORDER BY ts_ms DESC, set_number DESC, game_number DESC, point_number DESC
			) as rn
			FROM points
			WHERE match_ticker IN ?
		) WHERE rn = 1
	`, eventTickers).Scan(&apiScores).Error
	if err != nil {
		return nil, fmt.Errorf("latest scores: %w", err)
	}
	out := make(map[string]*LiveScore, len(eventTickers))
	for i := range apiScores {
		out[apiScores[i].EventTicker] = &apiScores[i]
	}

	// Fill gaps with Kalshi live-data scores for events not in API-Tennis.
	var kalshiScores []struct {
		EventTicker     string `gorm:"column:event_ticker"`
		Status          string `gorm:"column:status"`
		SetsHome        int    `gorm:"column:sets_home"`
		SetsAway        int    `gorm:"column:sets_away"`
		GamesHome       int    `gorm:"column:games_home"`
		GamesAway       int    `gorm:"column:games_away"`
		PointsHome      int    `gorm:"column:points_home"`
		PointsAway      int    `gorm:"column:points_away"`
		Server          int    `gorm:"column:server"`
		CompletedRounds int    `gorm:"column:completed_rounds"`
	}
	err = e.db.WithContext(ctx).Raw(`
		SELECT event_ticker, status, sets_home, sets_away, games_home, games_away,
		       points_home, points_away, server, completed_rounds
		FROM kalshi_scores WHERE event_ticker IN ?
	`, eventTickers).Scan(&kalshiScores).Error
	if err != nil {
		// Non-fatal — return API-Tennis scores only.
		return out, nil
	}
	for _, ks := range kalshiScores {
		if _, hasAPItennis := out[ks.EventTicker]; hasAPItennis {
			continue
		}
		// Skip not_started matches — zero scores would falsely mark them live.
		if ks.Status == "not_started" || ks.Status == "" {
			continue
		}
		currentSet := ks.CompletedRounds + 1
		isTB := ks.GamesHome == 6 && ks.GamesAway == 6
		isSetPoint := canWinSetKalshi(ks.GamesHome, ks.GamesAway, true) ||
			canWinSetKalshi(ks.GamesHome, ks.GamesAway, false)
		isMatchPoint := false
		if ks.SetsHome == 1 && canWinSetKalshi(ks.GamesHome, ks.GamesAway, true) {
			isMatchPoint = true
		}
		if ks.SetsAway == 1 && canWinSetKalshi(ks.GamesHome, ks.GamesAway, false) {
			isMatchPoint = true
		}
		isBreakPoint := false
		if ks.Server == 1 && canWinSetKalshi(ks.GamesHome, ks.GamesAway, false) {
			isBreakPoint = true
		}
		if ks.Server == 2 && canWinSetKalshi(ks.GamesHome, ks.GamesAway, true) {
			isBreakPoint = true
		}
		out[ks.EventTicker] = &LiveScore{
			EventTicker:  ks.EventTicker,
			SetNumber:    currentSet,
			GameNumber:   ks.GamesHome + ks.GamesAway + 1,
			PointNumber:  0,
			Server:       ks.Server,
			HomePoints:   kalshiPointToString(ks.PointsHome),
			AwayPoints:   kalshiPointToString(ks.PointsAway),
			HomeGames:    ks.GamesHome,
			AwayGames:    ks.GamesAway,
			HomeSetGames: ks.SetsHome,
			AwaySetGames: ks.SetsAway,
			IsTiebreak:   isTB,
			IsBreakPoint: isBreakPoint,
			IsSetPoint:   isSetPoint,
			IsMatchPoint: isMatchPoint,
		}
	}
	return out, nil
}

// kalshiPointToString converts Kalshi's integer point score to tennis notation.
// 0→"0", 15→"15", 30→"30", 40→"40", 50→"A".
func kalshiPointToString(n int) string {
	switch n {
	case 50:
		return "A"
	default:
		return strconv.Itoa(n)
	}
}

// canWinSetKalshi returns true if the given player can win the current set
// by winning the current game. Tennis set win: 6 games with 2-game margin,
// 7-5, or 7-6 (tiebreak).
func canWinSetKalshi(gamesHome, gamesAway int, home bool) bool {
	if home {
		newHome := gamesHome + 1
		if newHome >= 6 && newHome-gamesAway >= 2 {
			return true
		}
		if newHome == 7 && (gamesAway == 5 || gamesAway == 6) {
			return true
		}
		return false
	}
	newAway := gamesAway + 1
	if newAway >= 6 && newAway-gamesHome >= 2 {
		return true
	}
	if newAway == 7 && (gamesHome == 5 || gamesHome == 6) {
		return true
	}
	return false
}

// MarketTick is a single price point for a market, returned by GetEventTickPrices.
type MarketTick struct {
	TS    int64   `json:"ts"`
	Price float64 `json:"price"`
}

// MarketTickData holds tick data for one market in an event.
type MarketTickData struct {
	MarketTicker string       `json:"market_ticker"`
	PlayerName   string       `json:"player_name"`
	Ticks        []MarketTick `json:"ticks"`
}

// OrderRow is a single order for an event, returned by GetEventTickPrices.
type OrderRow struct {
	TS            int64   `json:"ts"`
	MarketTicker  string  `json:"market_ticker"`
	PlayerName    string  `json:"player_name"`
	Context       string  `json:"context"`
	MarketPrice   float64 `json:"market_price"`
	EdgeCents     int     `json:"edge_cents"`
	SuggestedSize float64 `json:"suggested_size"`
	Strategy      string  `json:"strategy"`
}

// ScoreEvent is a game-completion score snapshot, returned by GetEventTickPrices.
type ScoreEvent struct {
	TS           int64  `json:"ts"`
	SetNumber    int    `json:"set_number"`
	GameNumber   int    `json:"game_number"`
	HomeGames    int    `json:"home_games"`
	AwayGames    int    `json:"away_games"`
	HomePoints   string `json:"home_points"`
	AwayPoints   string `json:"away_points"`
	HomeSetGames int    `json:"home_set_games"`
	AwaySetGames int    `json:"away_set_games"`
}

// EventTickData holds tick data for all markets in an event.
type EventTickData struct {
	EventTicker string           `json:"event_ticker"`
	Title       string           `json:"title"`
	Markets     []MarketTickData `json:"markets"`
	Orders      []OrderRow       `json:"orders"`
	Scores      []ScoreEvent     `json:"scores"`
}

// GetEventTickPrices queries live tick prices for all markets in an event.
// Queries the DB directly so data is fresh even while ghost-trader is writing.
func (e *Engine) GetEventTickPrices(ctx context.Context, eventTicker string) (*EventTickData, error) {
	title := e.eventTitles[eventTicker]

	// Get markets for this event
	mkts, ok := e.markets[eventTicker]
	if !ok {
		// Not in cache — query DB directly
		var dbMkts []struct {
			MarketTicker string `gorm:"column:market_ticker"`
			PlayerName   string `gorm:"column:player_name"`
		}
		if err := e.db.WithContext(ctx).Raw(
			`SELECT market_ticker, player_name FROM markets WHERE event_ticker = ? ORDER BY market_ticker`,
			eventTicker).Scan(&dbMkts).Error; err != nil {
			return nil, fmt.Errorf("query markets: %w", err)
		}
		for _, m := range dbMkts {
			mkts = append(mkts, MarketRow{MarketTicker: m.MarketTicker, PlayerName: m.PlayerName})
		}
		if len(mkts) == 0 {
			return &EventTickData{EventTicker: eventTicker, Title: title}, nil
		}
	}

	result := &EventTickData{
		EventTicker: eventTicker,
		Title:       title,
		Markets:     make([]MarketTickData, 0, len(mkts)),
	}

	// Query orders for this event
	var orders []OrderRow
	if err := e.db.WithContext(ctx).Raw(
		`SELECT o.ts, o.market_ticker, m.player_name, o.context, o.market_price, o.edge_cents, o.suggested_size, o.strategy
		 FROM orders o LEFT JOIN markets m ON o.market_ticker = m.market_ticker
		 WHERE o.match_ticker = ? ORDER BY o.ts`, eventTicker).Scan(&orders).Error; err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	result.Orders = orders

	for _, m := range mkts {
		var ticks []MarketTick
		if err := e.db.WithContext(ctx).Raw(
			`SELECT ts, price FROM ticks WHERE market_ticker = ? AND price IS NOT NULL AND price > 0 ORDER BY ts`,
			m.MarketTicker).Scan(&ticks).Error; err != nil {
			return nil, fmt.Errorf("query ticks: %w", err)
		}
		result.Markets = append(result.Markets, MarketTickData{
			MarketTicker: m.MarketTicker,
			PlayerName:   m.PlayerName,
			Ticks:        ticks,
		})
	}

	// Query game-completion score events (last point per game)
	var scores []ScoreEvent
	if err := e.db.WithContext(ctx).Raw(
		`SELECT recv_ts as ts, set_number, game_number,
		        home_games, away_games, home_points, away_points,
		        COALESCE(home_set_games, 0) as home_set_games, COALESCE(away_set_games, 0) as away_set_games
		 FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY set_number, game_number
				ORDER BY point_number DESC
			) as rn
			FROM points
			WHERE match_ticker = ?
		 ) WHERE rn = 1
		 ORDER BY recv_ts`, eventTicker).Scan(&scores).Error; err != nil {
		return nil, fmt.Errorf("query score events: %w", err)
	}
	result.Scores = scores

	return result, nil
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

	// Load event titles + series
	var events []struct {
		EventTicker  string `gorm:"column:event_ticker"`
		Title        string `gorm:"column:title"`
		SeriesTicker string `gorm:"column:series_ticker"`
	}
	if err := e.db.WithContext(ctx).Raw(`SELECT event_ticker, title, series_ticker FROM events`).Scan(&events).Error; err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	for _, ev := range events {
		e.eventTitles[ev.EventTicker] = ev.Title
		e.eventSeries[ev.EventTicker] = ev.SeriesTicker
	}

	// Load surface from flashscore_matches
	var fsMatches []struct {
		EventTicker string `gorm:"column:event_ticker"`
		Surface     string `gorm:"column:surface"`
	}
	if err := e.db.WithContext(ctx).Raw(`SELECT event_ticker, surface FROM flashscore_matches WHERE event_ticker IS NOT NULL AND surface IS NOT NULL`).Scan(&fsMatches).Error; err != nil {
		return fmt.Errorf("query flashscore surfaces: %w", err)
	}
	for _, fm := range fsMatches {
		e.eventSurface[fm.EventTicker] = fm.Surface
	}

	// Load markets
	var mkts []struct {
		EventTicker  string `gorm:"column:event_ticker"`
		MarketTicker string `gorm:"column:market_ticker"`
		PlayerName   string `gorm:"column:player_name"`
		Result       string `gorm:"column:result"`
		Status       string `gorm:"column:status"`
		CloseTs      int64  `gorm:"column:close_ts"`
	}
	if err := e.db.WithContext(ctx).Raw(`SELECT event_ticker, market_ticker, player_name, result, status, close_ts FROM markets WHERE status = 'finalized' ORDER BY event_ticker, market_ticker`).Scan(&mkts).Error; err != nil {
		return fmt.Errorf("query markets: %w", err)
	}
	for _, m := range mkts {
		e.markets[m.EventTicker] = append(e.markets[m.EventTicker], MarketRow{m.MarketTicker, m.PlayerName, m.Result, m.Status})
		if m.CloseTs > 0 {
			e.marketCloseTs[m.EventTicker] = m.CloseTs
		}
	}

	// Load tick prices + dollar_volume in one streaming query (finalized markets only)
	tickRows, err := e.db.WithContext(ctx).Raw(`
		SELECT market_ticker, ts, price, dollar_volume
		FROM ticks
		WHERE market_ticker IN (SELECT market_ticker FROM markets WHERE status = 'finalized')
		  AND (price IS NOT NULL AND price > 0 OR dollar_volume IS NOT NULL AND dollar_volume > 0)
		ORDER BY market_ticker, ts
	`).Rows()
	if err != nil {
		return fmt.Errorf("query ticks: %w", err)
	}
	defer tickRows.Close()
	for tickRows.Next() {
		var mkt string
		var ts int64
		var price, dollarVol sql.NullFloat64
		if err := tickRows.Scan(&mkt, &ts, &price, &dollarVol); err != nil {
			return fmt.Errorf("scan tick row: %w", err)
		}
		if price.Valid && price.Float64 > 0 {
			e.tickPrices[mkt] = append(e.tickPrices[mkt], TickPrice{ts, price.Float64})
		}
		if dollarVol.Valid && dollarVol.Float64 > 0 {
			e.tickVolumes[mkt] = append(e.tickVolumes[mkt], TickVolume{ts, dollarVol.Float64})
		}
	}
	if err := tickRows.Err(); err != nil {
		return fmt.Errorf("iterate tick rows: %w", err)
	}

	// Load point-by-point score data
	var points []store.Point
	if err := e.db.WithContext(ctx).Raw(`
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0) as home_set_games, COALESCE(away_set_games, 0) as away_set_games,
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM points WHERE ts_ms IS NOT NULL
		  AND match_ticker IN (SELECT event_ticker FROM markets WHERE status = 'finalized')
		ORDER BY match_ticker, ts_ms
	`).Scan(&points).Error; err != nil {
		return fmt.Errorf("query points: %w", err)
	}
	for _, p := range points {
		e.points[p.MatchTicker] = append(e.points[p.MatchTicker], p)
	}

	return nil
}

// SeriesSetter is implemented by strategies that need series_ticker.
type SeriesSetter interface {
	SetSeriesTicker(eventTicker, seriesTicker string)
}

// SurfaceSetter is implemented by strategies that need court surface.
type SurfaceSetter interface {
	SetSurface(eventTicker, surface string)
}

// VolumeSetter is implemented by strategies that need dollar_volume series.
type VolumeSetter interface {
	SetVolumeSeries(marketTicker string, vols []algorithms.TickVolume)
}

// wireStrategyContext sets series, surface, and volume data on strategies
// that implement the corresponding setter interfaces.
func (e *Engine) wireStrategyContext(strat ReplayStrategy, matchTicker, homeMkt, awayMkt string) {
	if ss, ok := strat.(SeriesSetter); ok {
		if series := e.eventSeries[matchTicker]; series != "" {
			ss.SetSeriesTicker(matchTicker, series)
		}
	}
	if ss, ok := strat.(SurfaceSetter); ok {
		if surface := e.eventSurface[matchTicker]; surface != "" {
			ss.SetSurface(matchTicker, surface)
		}
	}
	if vs, ok := strat.(VolumeSetter); ok {
		if vols := e.tickVolumes[homeMkt]; len(vols) > 0 {
			vs.SetVolumeSeries(homeMkt, toAlgoVolumes(vols))
		}
		if vols := e.tickVolumes[awayMkt]; len(vols) > 0 {
			vs.SetVolumeSeries(awayMkt, toAlgoVolumes(vols))
		}
	}
}

// toAlgoVolumes converts engine TickVolume slice to algorithms.TickVolume slice.
func toAlgoVolumes(vols []TickVolume) []algorithms.TickVolume {
	out := make([]algorithms.TickVolume, len(vols))
	for i, v := range vols {
		out[i] = algorithms.TickVolume{TS: v.TS, DollarVolume: v.DollarVolume}
	}
	return out
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
		e.wireStrategyContext(strat, matchTicker, homeMkt, awayMkt)

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
			TS: o.TS,
		})
	}
	return orders
}

// replayInterleaved feeds price ticks and score events to a strategy in
// timestamp order. Price ticks from both markets are merged with point-by-point
// score data, then replayed chronologically. Score events are only fed to
// strategies implementing ScoreObserver.
func (e *Engine) replayInterleaved(strat ReplayStrategy, matchTicker, homeMkt, awayMkt string) {
	type event struct {
		ts    int64
		kind  int // 0=price, 1=score
		mkt   string
		price float64
		point store.Point
	}

	var events []event

	for _, mkt := range []string{homeMkt, awayMkt} {
		for _, t := range e.tickPrices[mkt] {
			events = append(events, event{ts: t.TS, kind: 0, mkt: mkt, price: t.Price})
		}
	}

	for _, p := range e.points[matchTicker] {
		events = append(events, event{ts: p.TS, kind: 1, point: p})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })

	scoreObs, _ := strat.(algorithms.ScoreObserver)

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

// PaperOrder is a simulated order with resolved P&L, for the dashboard.
type PaperOrder struct {
	ID            int64   `json:"id" gorm:"column:id"`
	TS            int64   `json:"ts" gorm:"column:ts"`
	MatchTicker   string  `json:"match_ticker" gorm:"column:match_ticker"`
	MarketTicker  string  `json:"market_ticker" gorm:"column:market_ticker"`
	PlayerName    string  `json:"player_name" gorm:"column:player_name"`
	Context       string  `json:"context" gorm:"column:context"`
	MarketPrice   float64 `json:"market_price" gorm:"column:market_price"`
	EdgeCents     int     `json:"edge_cents" gorm:"column:edge_cents"`
	SuggestedSize float64 `json:"suggested_size" gorm:"column:suggested_size"`
	Strategy      string  `json:"strategy" gorm:"column:strategy"`
	Result        string  `json:"result" gorm:"column:result"`
	Won           bool    `json:"won"`
	PnL           float64 `json:"pnl"`
}

// PaperOrderSummary holds aggregate stats for paper orders.
type PaperOrderSummary struct {
	TotalOrders   int     `json:"total_orders"`
	Resolved      int     `json:"resolved"`
	Wins          int     `json:"wins"`
	Losses        int     `json:"losses"`
	Pending       int     `json:"pending"`
	WinRate       float64 `json:"win_rate"`
	TotalInvested float64 `json:"total_invested"`
	NetPnL        float64 `json:"net_pnl"`
	ROI           float64 `json:"roi"`
}

// PaperOrderResponse is the full API response for /api/orders.
type PaperOrderResponse struct {
	Orders     []PaperOrder      `json:"orders"`
	Summary    PaperOrderSummary `json:"summary"`
	Strategies []string          `json:"strategies"` // all distinct strategies that have fired
	HasMore    bool              `json:"has_more"`
	NextCursor *PaperOrderCursor `json:"next_cursor,omitempty"`
}

// PaperOrderCursor is the keyset position of the last row in the current page.
// Clients pass it back as ?cursor_ts=<ts>&cursor_id=<id> for the next page.
type PaperOrderCursor struct {
	TS int64 `json:"ts"`
	ID int64 `json:"id"`
}

// GetPaperOrderSummary computes aggregate stats over all orders in a single
// SQL pass. Replaces the old Go loop that re-iterated every row.
func (e *Engine) GetPaperOrderSummary(ctx context.Context) (PaperOrderSummary, error) {
	var result struct {
		TotalOrders   int     `gorm:"column:total_orders"`
		Resolved      int     `gorm:"column:resolved"`
		Wins          int     `gorm:"column:wins"`
		Losses        int     `gorm:"column:losses"`
		Pending       int     `gorm:"column:pending"`
		TotalInvested float64 `gorm:"column:total_invested"`
		NetPnL        float64 `gorm:"column:net_pnl"`
	}
	err := e.db.WithContext(ctx).Raw(`
SELECT
  COUNT(*) as total_orders,
  SUM(CASE WHEN m.result IS NOT NULL AND m.result != '' THEN 1 ELSE 0 END) as resolved,
  SUM(CASE WHEN m.result = 'yes' THEN 1 ELSE 0 END) as wins,
  SUM(CASE WHEN m.result IS NOT NULL AND m.result != '' AND m.result != 'yes' THEN 1 ELSE 0 END) as losses,
  SUM(CASE WHEN m.result IS NULL OR m.result = '' THEN 1 ELSE 0 END) as pending,
  COALESCE(SUM(o.suggested_size * o.market_price), 0) as total_invested,
  COALESCE(SUM(CASE WHEN m.result = 'yes' THEN o.suggested_size * (1.0 - o.market_price)
                    WHEN m.result IS NOT NULL AND m.result != '' THEN -o.suggested_size * o.market_price
                    ELSE 0 END), 0) as net_pnl
FROM orders o LEFT JOIN markets m ON o.market_ticker = m.market_ticker`).Scan(&result).Error
	if err != nil {
		return PaperOrderSummary{}, fmt.Errorf("query paper order summary: %w", err)
	}
	s := PaperOrderSummary{
		TotalOrders:   result.TotalOrders,
		Resolved:      result.Resolved,
		Wins:          result.Wins,
		Losses:        result.Losses,
		Pending:       result.Pending,
		TotalInvested: result.TotalInvested,
		NetPnL:        result.NetPnL,
	}
	if s.Resolved > 0 {
		s.WinRate = float64(s.Wins) / float64(s.Resolved) * 100
	}
	if s.TotalInvested > 0 {
		s.ROI = s.NetPnL / s.TotalInvested * 100
	}
	return s, nil
}

// GetPaperOrderStrategies returns all distinct strategy names that have fired
// at least one order. Used to populate the dashboard filter sidebar regardless
// of which orders are currently loaded on the page.
func (e *Engine) GetPaperOrderStrategies(ctx context.Context) ([]string, error) {
	var out []string
	err := e.db.WithContext(ctx).Raw(`
SELECT DISTINCT strategy FROM orders
WHERE strategy != '' ORDER BY strategy`).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("query paper order strategies: %w", err)
	}
	return out, nil
}

// GetPaperOrdersPage returns one keyset-paginated page of paper orders, newest
// first. cursor is the (ts, id) of the last row from the previous page; pass
// nil for the first page. limit is clamped to [1, 1000].
// has_more is true when the page is full; next_cursor is the position to use
// for the following page (nil when has_more is false).
func (e *Engine) GetPaperOrdersPage(ctx context.Context, cursor *PaperOrderCursor, limit int) ([]PaperOrder, bool, *PaperOrderCursor, error) {
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	var orders []PaperOrder
	if cursor == nil {
		err := e.db.WithContext(ctx).Raw(`
SELECT o.ts, o.id, o.match_ticker, o.market_ticker, o.context,
       o.market_price, o.edge_cents, o.suggested_size, o.strategy,
       m.player_name, m.result
FROM orders o
LEFT JOIN markets m ON o.market_ticker = m.market_ticker
ORDER BY o.ts DESC, o.id DESC
LIMIT ?`, limit+1).Scan(&orders).Error
		if err != nil {
			return nil, false, nil, fmt.Errorf("query paper orders page: %w", err)
		}
	} else {
		err := e.db.WithContext(ctx).Raw(`
SELECT o.ts, o.id, o.match_ticker, o.market_ticker, o.context,
       o.market_price, o.edge_cents, o.suggested_size, o.strategy,
       m.player_name, m.result
FROM orders o
LEFT JOIN markets m ON o.market_ticker = m.market_ticker
WHERE (o.ts, o.id) < (?, ?)
ORDER BY o.ts DESC, o.id DESC
LIMIT ?`, cursor.TS, cursor.ID, limit+1).Scan(&orders).Error
		if err != nil {
			return nil, false, nil, fmt.Errorf("query paper orders page: %w", err)
		}
	}

	for i := range orders {
		orders[i].Won = orders[i].Result == "yes"
		if orders[i].Result != "" {
			if orders[i].Won {
				orders[i].PnL = orders[i].SuggestedSize * (1.0 - orders[i].MarketPrice)
			} else {
				orders[i].PnL = -orders[i].SuggestedSize * orders[i].MarketPrice
			}
		}
	}

	hasMore := len(orders) > limit
	if hasMore {
		orders = orders[:limit]
	}
	var next *PaperOrderCursor
	if hasMore {
		last := orders[len(orders)-1]
		next = &PaperOrderCursor{TS: last.TS, ID: last.ID}
	}
	return orders, hasMore, next, nil
}

// GetOrderCountsByEvent returns a map of event_ticker → simulated order count.
func (e *Engine) GetOrderCountsByEvent(ctx context.Context) (map[string]int, error) {
	var results []struct {
		MatchTicker string `gorm:"column:match_ticker"`
		Count       int    `gorm:"column:count"`
	}
	err := e.db.WithContext(ctx).Raw(`SELECT match_ticker, COUNT(*) as count FROM orders GROUP BY match_ticker`).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("query order counts: %w", err)
	}
	counts := make(map[string]int, len(results))
	for _, r := range results {
		counts[r.MatchTicker] = r.Count
	}
	return counts, nil
}

// PassedMatch is a finalized event with winner + aggregate P&L, for the dashboard.
type PassedMatch struct {
	EventTicker string  `json:"event_ticker" gorm:"column:event_ticker"`
	Title       string  `json:"title" gorm:"column:title"`
	Series      string  `json:"series" gorm:"column:series"`
	Winner      string  `json:"winner" gorm:"column:winner"`
	CloseTs     int64   `json:"close_ts" gorm:"column:close_ts"`
	SettledTs   int64   `json:"settled_ts" gorm:"column:settled_ts"`
	OrderCount  int     `json:"order_count"`
	NetPnL      float64 `json:"net_pnl"`
}

// GetPassedMatches returns events where both markets are finalized, newest first.
// Joins orders for sim count + resolved P&L per event.
func (e *Engine) GetPassedMatches(ctx context.Context, limit int) ([]PassedMatch, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []PassedMatch
	err := e.db.WithContext(ctx).Raw(`
SELECT e.event_ticker, e.title, e.series_ticker as series,
       (SELECT player_name FROM markets WHERE event_ticker = e.event_ticker AND result = 'yes' LIMIT 1) as winner,
       MAX(mk.close_ts) as close_ts, MAX(mk.settlement_ts) as settled_ts
FROM events e
JOIN markets mk ON mk.event_ticker = e.event_ticker
WHERE mk.status = 'finalized'
  AND NOT EXISTS (
    SELECT 1 FROM markets WHERE event_ticker = e.event_ticker AND status != 'finalized'
  )
GROUP BY e.event_ticker, e.title, e.series_ticker
ORDER BY MAX(mk.settlement_ts) DESC
LIMIT ?`, limit).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("query passed matches: %w", err)
	}
	if len(out) == 0 {
		return out, nil
	}

	tickers := make([]string, len(out))
	idx := make(map[string]int, len(out))
	for i, pm := range out {
		tickers[i] = pm.EventTicker
		idx[pm.EventTicker] = i
	}

	var orderAggs []struct {
		MatchTicker string  `gorm:"column:match_ticker"`
		Count       int     `gorm:"column:count"`
		NetPnL      float64 `gorm:"column:net_pnl"`
	}
	err = e.db.WithContext(ctx).Raw(`
SELECT o.match_ticker, COUNT(*) as count,
       COALESCE(SUM(CASE WHEN m.result = 'yes' THEN o.suggested_size * (1.0 - o.market_price)
                WHEN m.result = 'no'  THEN -o.suggested_size * o.market_price
                ELSE 0 END), 0) as net_pnl
FROM orders o
LEFT JOIN markets m ON o.market_ticker = m.market_ticker
WHERE o.match_ticker IN ?
GROUP BY o.match_ticker`, tickers).Scan(&orderAggs).Error
	if err != nil {
		return nil, fmt.Errorf("query passed order aggregates: %w", err)
	}
	for _, oa := range orderAggs {
		if i, ok := idx[oa.MatchTicker]; ok {
			out[i].OrderCount = oa.Count
			out[i].NetPnL = oa.NetPnL
		}
	}
	return out, nil
}

// GetPendingOrderCountsByEvent returns a map of event_ticker → unsettled order count.
func (e *Engine) GetPendingOrderCountsByEvent(ctx context.Context) (map[string]int, error) {
	var results []struct {
		MatchTicker string `gorm:"column:match_ticker"`
		Count       int    `gorm:"column:count"`
	}
	err := e.db.WithContext(ctx).Raw(`SELECT o.match_ticker, COUNT(*) as count
		 FROM orders o LEFT JOIN markets m ON o.market_ticker = m.market_ticker
		 WHERE m.result IS NULL OR m.result = ''
		 GROUP BY o.match_ticker`).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("query pending order counts: %w", err)
	}
	counts := make(map[string]int, len(results))
	for _, r := range results {
		counts[r.MatchTicker] = r.Count
	}
	return counts, nil
}
