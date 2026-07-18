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
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
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
	db            *sql.DB
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
		tickVolumes:   make(map[string][]TickVolume),
		points:        make(map[string][]store.Point),
		eventTitles:   make(map[string]string),
		eventSeries:   make(map[string]string),
		eventSurface:  make(map[string]string),
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
	placeholders := strings.Repeat("?,", len(eventTickers))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(eventTickers))
	for i, t := range eventTickers {
		args[i] = t
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT event_ticker, MAX(occurrence_ts) FROM markets WHERE event_ticker IN (`+placeholders+`) GROUP BY event_ticker`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, len(eventTickers))
	for rows.Next() {
		var et string
		var ts sql.NullInt64
		if err := rows.Scan(&et, &ts); err != nil {
			return nil, err
		}
		out[et] = ts.Int64
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
	placeholders := strings.Repeat("?,", len(eventTickers))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(eventTickers))
	for i, t := range eventTickers {
		args[i] = t
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT m.event_ticker, MAX(t.ts)
		 FROM ticks t JOIN markets m ON t.market_ticker = m.market_ticker
		 WHERE m.event_ticker IN (`+placeholders+`) GROUP BY m.event_ticker`, args...)
	if err != nil {
		return nil, fmt.Errorf("latest tick ts: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int64, len(eventTickers))
	for rows.Next() {
		var et string
		var ts sql.NullInt64
		if err := rows.Scan(&et, &ts); err != nil {
			return nil, err
		}
		out[et] = ts.Int64
	}
	return out, rows.Err()
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
	placeholders := strings.Repeat("?,", len(eventTickers))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(eventTickers))
	for i, t := range eventTickers {
		args[i] = t
	}
	query := fmt.Sprintf(`
		SELECT match_ticker, set_number, game_number, point_number,
		       server, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0), COALESCE(away_set_games, 0),
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY match_ticker
				ORDER BY ts_ms DESC, set_number DESC, game_number DESC, point_number DESC
			) as rn
			FROM points
			WHERE match_ticker IN (%s)
		) WHERE rn = 1
	`, placeholders)
	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("latest scores: %w", err)
	}
	defer rows.Close()
	out := make(map[string]*LiveScore, len(eventTickers))
	for rows.Next() {
		var s LiveScore
		var isTB, isBP, isSP, isMP int
		if err := rows.Scan(
			&s.EventTicker, &s.SetNumber, &s.GameNumber, &s.PointNumber,
			&s.Server, &s.HomePoints, &s.AwayPoints,
			&s.HomeGames, &s.AwayGames, &s.HomeSetGames, &s.AwaySetGames,
			&isTB, &isBP, &isSP, &isMP,
		); err != nil {
			return nil, fmt.Errorf("scan live score: %w", err)
		}
		s.IsTiebreak = isTB != 0
		s.IsBreakPoint = isBP != 0
		s.IsSetPoint = isSP != 0
		s.IsMatchPoint = isMP != 0
		out[s.EventTicker] = &s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill gaps with Kalshi live-data scores for events not in API-Tennis.
	kalshiScores, err := e.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT event_ticker, status, sets_home, sets_away, games_home, games_away,
		       points_home, points_away, server, completed_rounds
		FROM kalshi_scores WHERE event_ticker IN (%s)
	`, placeholders), args...)
	if err != nil {
		// Non-fatal — return API-Tennis scores only.
		return out, nil
	}
	defer kalshiScores.Close()
	for kalshiScores.Next() {
		var et, status string
		var setsHome, setsAway, gamesHome, gamesAway, pointsHome, pointsAway, server, completedRounds int
		if err := kalshiScores.Scan(&et, &status, &setsHome, &setsAway,
			&gamesHome, &gamesAway, &pointsHome, &pointsAway,
			&server, &completedRounds); err != nil {
			continue
		}
		if _, hasAPItennis := out[et]; hasAPItennis {
			continue
		}
		// Skip not_started matches — zero scores would falsely mark them live.
		if status == "not_started" || status == "" {
			continue
		}
		currentSet := completedRounds + 1
		isTB := gamesHome == 6 && gamesAway == 6
		isSetPoint := canWinSetKalshi(gamesHome, gamesAway, true) ||
			canWinSetKalshi(gamesHome, gamesAway, false)
		isMatchPoint := false
		if setsHome == 1 && canWinSetKalshi(gamesHome, gamesAway, true) {
			isMatchPoint = true
		}
		if setsAway == 1 && canWinSetKalshi(gamesHome, gamesAway, false) {
			isMatchPoint = true
		}
		isBreakPoint := false
		if server == 1 && canWinSetKalshi(gamesHome, gamesAway, false) {
			isBreakPoint = true
		}
		if server == 2 && canWinSetKalshi(gamesHome, gamesAway, true) {
			isBreakPoint = true
		}
		out[et] = &LiveScore{
			EventTicker:  et,
			SetNumber:    currentSet,
			GameNumber:   gamesHome + gamesAway + 1,
			PointNumber:  0,
			Server:       server,
			HomePoints:   kalshiPointToString(pointsHome),
			AwayPoints:   kalshiPointToString(pointsAway),
			HomeGames:    gamesHome,
			AwayGames:    gamesAway,
			HomeSetGames: setsHome,
			AwaySetGames: setsAway,
			IsTiebreak:   isTB,
			IsBreakPoint: isBreakPoint,
			IsSetPoint:   isSetPoint,
			IsMatchPoint: isMatchPoint,
		}
	}
	return out, kalshiScores.Err()
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
		rows, err := e.db.QueryContext(ctx,
			`SELECT market_ticker, player_name FROM markets WHERE event_ticker = ? ORDER BY market_ticker`,
			eventTicker)
		if err != nil {
			return nil, fmt.Errorf("query markets: %w", err)
		}
		for rows.Next() {
			var mt, pn string
			if err := rows.Scan(&mt, &pn); err != nil {
				rows.Close()
				return nil, err
			}
			mkts = append(mkts, MarketRow{MarketTicker: mt, PlayerName: pn})
		}
		rows.Close()
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
	orderRows, err := e.db.QueryContext(ctx,
		`SELECT o.ts, o.market_ticker, m.player_name, o.context, o.market_price, o.edge_cents, o.suggested_size, o.strategy
		 FROM orders o LEFT JOIN markets m ON o.market_ticker = m.market_ticker
		 WHERE o.match_ticker = ? ORDER BY o.ts`, eventTicker)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	for orderRows.Next() {
		var o OrderRow
		var playerName sql.NullString
		if err := orderRows.Scan(&o.TS, &o.MarketTicker, &playerName, &o.Context, &o.MarketPrice,
			&o.EdgeCents, &o.SuggestedSize, &o.Strategy); err != nil {
			orderRows.Close()
			return nil, err
		}
		o.PlayerName = playerName.String
		result.Orders = append(result.Orders, o)
	}
	orderRows.Close()

	for _, m := range mkts {
		rows, err := e.db.QueryContext(ctx,
			`SELECT ts, price FROM ticks WHERE market_ticker = ? AND price IS NOT NULL AND price > 0 ORDER BY ts`,
			m.MarketTicker)
		if err != nil {
			return nil, fmt.Errorf("query ticks: %w", err)
		}
		var ticks []MarketTick
		for rows.Next() {
			var ts int64
			var price float64
			if err := rows.Scan(&ts, &price); err != nil {
				rows.Close()
				return nil, err
			}
			ticks = append(ticks, MarketTick{TS: ts, Price: price})
		}
		rows.Close()
		result.Markets = append(result.Markets, MarketTickData{
			MarketTicker: m.MarketTicker,
			PlayerName:   m.PlayerName,
			Ticks:        ticks,
		})
	}

	// Query game-completion score events (last point per game)
	scoreRows, err := e.db.QueryContext(ctx,
		`SELECT recv_ts, set_number, game_number,
		        home_games, away_games, home_points, away_points,
		        COALESCE(home_set_games, 0), COALESCE(away_set_games, 0)
		 FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY set_number, game_number
				ORDER BY point_number DESC
			) as rn
			FROM points
			WHERE match_ticker = ?
		 ) WHERE rn = 1
		 ORDER BY recv_ts`, eventTicker)
	if err != nil {
		return nil, fmt.Errorf("query score events: %w", err)
	}
	for scoreRows.Next() {
		var s ScoreEvent
		if err := scoreRows.Scan(&s.TS, &s.SetNumber, &s.GameNumber,
			&s.HomeGames, &s.AwayGames, &s.HomePoints, &s.AwayPoints,
			&s.HomeSetGames, &s.AwaySetGames); err != nil {
			scoreRows.Close()
			return nil, err
		}
		result.Scores = append(result.Scores, s)
	}
	scoreRows.Close()

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
	rows, err := e.db.QueryContext(ctx, `SELECT event_ticker, title, series_ticker FROM events`)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	for rows.Next() {
		var et, title, series string
		if err := rows.Scan(&et, &title, &series); err != nil {
			rows.Close()
			return err
		}
		e.eventTitles[et] = title
		e.eventSeries[et] = series
	}
	rows.Close()

	// Load surface from flashscore_matches
	fsRows, err := e.db.QueryContext(ctx,
		`SELECT event_ticker, surface FROM flashscore_matches WHERE event_ticker IS NOT NULL AND surface IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query flashscore surfaces: %w", err)
	}
	for fsRows.Next() {
		var et, surface string
		if err := fsRows.Scan(&et, &surface); err != nil {
			fsRows.Close()
			return err
		}
		e.eventSurface[et] = surface
	}
	fsRows.Close()

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

	// Load dollar_volume time series per market (for volratio strategy)
	vRows, err := e.db.QueryContext(ctx,
		`SELECT market_ticker, ts, dollar_volume FROM ticks
		 WHERE dollar_volume IS NOT NULL AND dollar_volume > 0
		 ORDER BY market_ticker, ts`)
	if err != nil {
		return fmt.Errorf("query tick volumes: %w", err)
	}
	for vRows.Next() {
		var mt string
		var ts int64
		var dv float64
		if err := vRows.Scan(&mt, &ts, &dv); err != nil {
			vRows.Close()
			return err
		}
		e.tickVolumes[mt] = append(e.tickVolumes[mt], TickVolume{ts, dv})
	}
	vRows.Close()

	// Load point-by-point score data
	pRows, err := e.db.QueryContext(ctx, `
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0), COALESCE(away_set_games, 0),
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM points WHERE ts_ms IS NOT NULL
		ORDER BY match_ticker, ts_ms
	`)
	if err != nil {
		return fmt.Errorf("query points: %w", err)
	}
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
			return err
		}
		p.IsTiebreak = isTB != 0
		p.IsBreakPoint = isBP != 0
		p.IsSetPoint = isSP != 0
		p.IsMatchPoint = isMP != 0
		e.points[mt] = append(e.points[mt], p)
	}
	pRows.Close()

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
	TS            int64   `json:"ts"`
	MatchTicker   string  `json:"match_ticker"`
	MarketTicker  string  `json:"market_ticker"`
	PlayerName    string  `json:"player_name"`
	Context       string  `json:"context"`
	MarketPrice   float64 `json:"market_price"`
	EdgeCents     int     `json:"edge_cents"`
	SuggestedSize float64 `json:"suggested_size"`
	Strategy      string  `json:"strategy"`
	Result        string  `json:"result"`
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
	Orders  []PaperOrder      `json:"orders"`
	Summary PaperOrderSummary `json:"summary"`
}

// GetAllPaperOrders queries all orders from the DB, joins with markets for
// result/P&L, and returns them sorted by ts descending.
func (e *Engine) GetAllPaperOrders(ctx context.Context) (*PaperOrderResponse, error) {
	rows, err := e.db.QueryContext(ctx, `
SELECT o.ts, o.match_ticker, o.market_ticker, o.context,
       o.market_price, o.edge_cents, o.suggested_size, o.strategy,
       m.player_name, m.result
FROM orders o
LEFT JOIN markets m ON o.market_ticker = m.market_ticker
ORDER BY o.ts DESC`)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()

	var orders []PaperOrder
	s := PaperOrderSummary{}

	for rows.Next() {
		var o PaperOrder
		var playerName, result sql.NullString
		if err := rows.Scan(&o.TS, &o.MatchTicker, &o.MarketTicker, &o.Context,
			&o.MarketPrice, &o.EdgeCents, &o.SuggestedSize, &o.Strategy,
			&playerName, &result); err != nil {
			return nil, err
		}
		o.PlayerName = playerName.String
		o.Result = result.String
		o.Won = result.String == "yes"

		s.TotalOrders++
		s.TotalInvested += o.SuggestedSize * o.MarketPrice

		if result.Valid && result.String != "" {
			s.Resolved++
			if o.Won {
				s.Wins++
				o.PnL = o.SuggestedSize * (1.0 - o.MarketPrice)
			} else {
				s.Losses++
				o.PnL = -o.SuggestedSize * o.MarketPrice
			}
			s.NetPnL += o.PnL
		} else {
			s.Pending++
		}

		orders = append(orders, o)
	}

	if s.Resolved > 0 {
		s.WinRate = float64(s.Wins) / float64(s.Resolved) * 100
	}
	if s.TotalInvested > 0 {
		s.ROI = s.NetPnL / s.TotalInvested * 100
	}

	return &PaperOrderResponse{Orders: orders, Summary: s}, nil
}

// GetOrderCountsByEvent returns a map of event_ticker → simulated order count.
func (e *Engine) GetOrderCountsByEvent(ctx context.Context) (map[string]int, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT match_ticker, COUNT(*) FROM orders GROUP BY match_ticker`)
	if err != nil {
		return nil, fmt.Errorf("query order counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var ticker string
		var count int
		if err := rows.Scan(&ticker, &count); err != nil {
			return nil, err
		}
		counts[ticker] = count
	}
	return counts, nil
}

// PassedMatch is a finalized event with winner + aggregate P&L, for the dashboard.
type PassedMatch struct {
	EventTicker string  `json:"event_ticker"`
	Title       string  `json:"title"`
	Series      string  `json:"series"`
	Winner      string  `json:"winner"`
	CloseTs     int64   `json:"close_ts"`
	SettledTs   int64   `json:"settled_ts"`
	OrderCount  int     `json:"order_count"`
	NetPnL      float64 `json:"net_pnl"`
}

// GetPassedMatches returns events where both markets are finalized, newest first.
// Joins orders for sim count + resolved P&L per event.
func (e *Engine) GetPassedMatches(ctx context.Context, limit int) ([]PassedMatch, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := e.db.QueryContext(ctx, `
SELECT e.event_ticker, e.title, e.series_ticker,
       (SELECT player_name FROM markets WHERE event_ticker = e.event_ticker AND result = 'yes' LIMIT 1),
       MAX(mk.close_ts), MAX(mk.settlement_ts)
FROM events e
JOIN markets mk ON mk.event_ticker = e.event_ticker
WHERE mk.status = 'finalized'
  AND NOT EXISTS (
    SELECT 1 FROM markets WHERE event_ticker = e.event_ticker AND status != 'finalized'
  )
GROUP BY e.event_ticker, e.title, e.series_ticker
ORDER BY MAX(mk.settlement_ts) DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query passed matches: %w", err)
	}
	defer rows.Close()

	out := make([]PassedMatch, 0, limit)
	for rows.Next() {
		var pm PassedMatch
		var closeTs, settledTs sql.NullInt64
		var winner sql.NullString
		if err := rows.Scan(&pm.EventTicker, &pm.Title, &pm.Series, &winner, &closeTs, &settledTs); err != nil {
			return nil, err
		}
		pm.Winner = winner.String
		pm.CloseTs = closeTs.Int64
		pm.SettledTs = settledTs.Int64
		out = append(out, pm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	tickers := make([]any, len(out))
	idx := make(map[string]int, len(out))
	for i, pm := range out {
		tickers[i] = pm.EventTicker
		idx[pm.EventTicker] = i
	}
	placeholders := strings.Repeat("?,", len(out))
	placeholders = placeholders[:len(placeholders)-1]

	oRows, err := e.db.QueryContext(ctx, fmt.Sprintf(`
SELECT o.match_ticker, COUNT(*),
       SUM(CASE WHEN m.result = 'yes' THEN o.suggested_size * (1.0 - o.market_price)
                WHEN m.result = 'no'  THEN -o.suggested_size * o.market_price
                ELSE 0 END)
FROM orders o
LEFT JOIN markets m ON o.market_ticker = m.market_ticker
WHERE o.match_ticker IN (%s)
GROUP BY o.match_ticker`, placeholders), tickers...)
	if err != nil {
		return nil, fmt.Errorf("query passed order aggregates: %w", err)
	}
	defer oRows.Close()
	for oRows.Next() {
		var ticker string
		var count int
		var pnl sql.NullFloat64
		if err := oRows.Scan(&ticker, &count, &pnl); err != nil {
			return nil, err
		}
		if i, ok := idx[ticker]; ok {
			out[i].OrderCount = count
			out[i].NetPnL = pnl.Float64
		}
	}
	return out, oRows.Err()
}

// GetPendingOrderCountsByEvent returns a map of event_ticker → unsettled order count.
func (e *Engine) GetPendingOrderCountsByEvent(ctx context.Context) (map[string]int, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT o.match_ticker, COUNT(*)
		 FROM orders o LEFT JOIN markets m ON o.market_ticker = m.market_ticker
		 WHERE m.result IS NULL OR m.result = ''
		 GROUP BY o.match_ticker`)
	if err != nil {
		return nil, fmt.Errorf("query pending order counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var ticker string
		var count int
		if err := rows.Scan(&ticker, &count); err != nil {
			return nil, err
		}
		counts[ticker] = count
	}
	return counts, nil
}
