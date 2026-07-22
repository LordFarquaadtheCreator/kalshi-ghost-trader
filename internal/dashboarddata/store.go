// Package dashboarddata provides live DB queries for the dashboard API.
// Extracted from the backtest engine so the backtest package is replay-only
// and the dashboard has its own data layer that does not reach through Engine.
package dashboarddata

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// scoreRecencyWindow bounds how old a score row can be while still counting
// as "live". Matches finished on court but not yet settled on Kalshi stay
// subscribed; without this filter their last point/score row keeps them
// falsely marked live forever. 10 min covers between-sets breaks and short
// rain delays without dropping a genuinely live match mid-game.
const scoreRecencyWindow = 10 * time.Minute

// LiveStore serves dashboard queries directly from the PostgreSQL DB.
// It caches event titles (loaded once at construction) for cheap lookup
// in hot paths like the tracked-matches handler.
type LiveStore struct {
	db           *gorm.DB
	log          *slog.Logger
	eventTitles  map[string]string
}

// NewLiveStore creates a LiveStore over an existing gorm DB handle.
// Loads event titles once for caching. Callers should reuse the store.DB
// gorm handle rather than opening a second connection.
func NewLiveStore(db *gorm.DB, log *slog.Logger) (*LiveStore, error) {
	s := &LiveStore{
		db:          db,
		log:         log,
		eventTitles: make(map[string]string),
	}
	if err := s.loadEventTitles(context.Background()); err != nil {
		return nil, fmt.Errorf("dashboarddata: load event titles: %w", err)
	}
	return s, nil
}

func (s *LiveStore) loadEventTitles(ctx context.Context) error {
	var events []struct {
		EventTicker string `gorm:"column:event_ticker"`
		Title       string `gorm:"column:title"`
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT event_ticker, title FROM events`).Scan(&events).Error; err != nil {
		return err
	}
	for _, ev := range events {
		s.eventTitles[ev.EventTicker] = ev.Title
	}
	return nil
}

// EventTitle returns the cached title for an event, or empty string if unknown.
func (s *LiveStore) EventTitle(eventTicker string) string {
	return s.eventTitles[eventTicker]
}

// EventOccurrenceTS returns a map of event_ticker -> occurrence_ts for the given events.
// Queries the DB live so data is fresh.
func (s *LiveStore) EventOccurrenceTS(ctx context.Context, eventTickers []string) (map[string]int64, error) {
	if len(eventTickers) == 0 {
		return nil, nil
	}
	var results []struct {
		EventTicker  string `gorm:"column:event_ticker"`
		OccurrenceTS int64  `gorm:"column:occurrence_ts"`
	}
	err := s.db.WithContext(ctx).Raw(
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
func (s *LiveStore) LatestTickTS(ctx context.Context, eventTickers []string) (map[string]int64, error) {
	if len(eventTickers) == 0 {
		return nil, nil
	}
	var results []struct {
		EventTicker string `gorm:"column:event_ticker"`
		TS          int64  `gorm:"column:ts"`
	}
	err := s.db.WithContext(ctx).Raw(
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
//
// Only scores fresher than scoreRecencyWindow are returned. Stale rows from
// matches that finished on court but haven't settled on Kalshi yet would
// otherwise keep the dashboard falsely marking them "live".
func (s *LiveStore) LatestScores(ctx context.Context, eventTickers []string) (map[string]*LiveScore, error) {
	if len(eventTickers) == 0 {
		return nil, nil
	}
	cutoffMs := time.Now().Add(-scoreRecencyWindow).UnixMilli()
	var apiScores []LiveScore
	err := s.db.WithContext(ctx).Raw(`
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
			WHERE match_ticker IN ? AND recv_ts >= ?
		) WHERE rn = 1
	`, eventTickers, cutoffMs).Scan(&apiScores).Error
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
	err = s.db.WithContext(ctx).Raw(`
		SELECT event_ticker, status, sets_home, sets_away, games_home, games_away,
		       points_home, points_away, server, completed_rounds
		FROM kalshi_scores WHERE event_ticker IN ? AND updated_ts >= ?
	`, eventTickers, cutoffMs).Scan(&kalshiScores).Error
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
// 0->"0", 15->"15", 30->"30", 40->"40", 50->"A".
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

// MarketTick is a single price point for a market.
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

// ScoreEvent is a game-completion score snapshot.
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
func (s *LiveStore) GetEventTickPrices(ctx context.Context, eventTicker string) (*EventTickData, error) {
	title := s.eventTitles[eventTicker]

	// Get markets for this event
	var dbMkts []struct {
		MarketTicker string `gorm:"column:market_ticker"`
		PlayerName   string `gorm:"column:player_name"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT market_ticker, player_name FROM markets WHERE event_ticker = ? ORDER BY market_ticker`,
		eventTicker).Scan(&dbMkts).Error; err != nil {
		return nil, fmt.Errorf("query markets: %w", err)
	}

	result := &EventTickData{
		EventTicker: eventTicker,
		Title:       title,
		Markets:     make([]MarketTickData, 0, len(dbMkts)),
	}

	if len(dbMkts) == 0 {
		return result, nil
	}

	// Query orders for this event
	var orders []OrderRow
	if err := s.db.WithContext(ctx).Raw(
		`SELECT o.ts, o.market_ticker, m.player_name, o.context, o.market_price, o.edge_cents, o.suggested_size, o.strategy
		 FROM orders o LEFT JOIN markets m ON o.market_ticker = m.market_ticker
		 WHERE o.match_ticker = ? ORDER BY o.ts`, eventTicker).Scan(&orders).Error; err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	result.Orders = orders

	for _, m := range dbMkts {
		var ticks []MarketTick
		if err := s.db.WithContext(ctx).Raw(
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
	if err := s.db.WithContext(ctx).Raw(
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
	Strategies []string          `json:"strategies"`
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
// SQL pass.
func (s *LiveStore) GetPaperOrderSummary(ctx context.Context) (PaperOrderSummary, error) {
	var result struct {
		TotalOrders   int     `gorm:"column:total_orders"`
		Resolved      int     `gorm:"column:resolved"`
		Wins          int     `gorm:"column:wins"`
		Losses        int     `gorm:"column:losses"`
		Pending       int     `gorm:"column:pending"`
		TotalInvested float64 `gorm:"column:total_invested"`
		NetPnL        float64 `gorm:"column:net_pnl"`
	}
	err := s.db.WithContext(ctx).Raw(`
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
	p := PaperOrderSummary{
		TotalOrders:   result.TotalOrders,
		Resolved:      result.Resolved,
		Wins:          result.Wins,
		Losses:        result.Losses,
		Pending:       result.Pending,
		TotalInvested: result.TotalInvested,
		NetPnL:        result.NetPnL,
	}
	if p.Resolved > 0 {
		p.WinRate = float64(p.Wins) / float64(p.Resolved) * 100
	}
	if p.TotalInvested > 0 {
		p.ROI = p.NetPnL / p.TotalInvested * 100
	}
	return p, nil
}

// GetPaperOrderStrategies returns all distinct strategy names that have fired
// at least one order.
func (s *LiveStore) GetPaperOrderStrategies(ctx context.Context) ([]string, error) {
	var out []string
	err := s.db.WithContext(ctx).Raw(`
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
func (s *LiveStore) GetPaperOrdersPage(ctx context.Context, cursor *PaperOrderCursor, limit int) ([]PaperOrder, bool, *PaperOrderCursor, error) {
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	var orders []PaperOrder
	if cursor == nil {
		err := s.db.WithContext(ctx).Raw(`
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
		err := s.db.WithContext(ctx).Raw(`
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

// GetOrderCountsByEvent returns a map of event_ticker -> simulated order count.
func (s *LiveStore) GetOrderCountsByEvent(ctx context.Context) (map[string]int, error) {
	var results []struct {
		MatchTicker string `gorm:"column:match_ticker"`
		Count       int    `gorm:"column:count"`
	}
	err := s.db.WithContext(ctx).Raw(`SELECT match_ticker, COUNT(*) as count FROM orders GROUP BY match_ticker`).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("query order counts: %w", err)
	}
	counts := make(map[string]int, len(results))
	for _, r := range results {
		counts[r.MatchTicker] = r.Count
	}
	return counts, nil
}

// GetPendingOrderCountsByEvent returns a map of event_ticker -> unsettled order count.
func (s *LiveStore) GetPendingOrderCountsByEvent(ctx context.Context) (map[string]int, error) {
	var results []struct {
		MatchTicker string `gorm:"column:match_ticker"`
		Count       int    `gorm:"column:count"`
	}
	err := s.db.WithContext(ctx).Raw(`SELECT o.match_ticker, COUNT(*) as count
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
func (s *LiveStore) GetPassedMatches(ctx context.Context, limit int) ([]PassedMatch, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []PassedMatch
	err := s.db.WithContext(ctx).Raw(`
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
	err = s.db.WithContext(ctx).Raw(`
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
