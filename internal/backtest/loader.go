package backtest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// load fetches all finalized markets, tick prices, tick volumes, and
// point-by-point score data from the DB into memory for replay.
// Called once at Engine construction.
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

	// Load markets (finalized only — replay needs settled results for PnL)
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

	// Load book data (bid/ask/sizes) from ticker messages for book-pressure strategies.
	bookRows, err := e.db.WithContext(ctx).Raw(`
		SELECT market_ticker, ts, yes_bid, yes_ask, yes_bid_size, yes_ask_size
		FROM ticks
		WHERE market_ticker IN (SELECT market_ticker FROM markets WHERE status = 'finalized')
		  AND msg_type = 'ticker'
		  AND yes_bid > 0 AND yes_ask > 0
		  AND yes_bid_size > 0 AND yes_ask_size > 0
		ORDER BY market_ticker, ts
	`).Rows()
	if err != nil {
		return fmt.Errorf("query book ticks: %w", err)
	}
	defer bookRows.Close()
	for bookRows.Next() {
		var mkt string
		var ts int64
		var bid, ask, bidSz, askSz sql.NullFloat64
		if err := bookRows.Scan(&mkt, &ts, &bid, &ask, &bidSz, &askSz); err != nil {
			return fmt.Errorf("scan book row: %w", err)
		}
		if !bid.Valid || !ask.Valid || !bidSz.Valid || !askSz.Valid {
			continue
		}
		e.bookTicks[mkt] = append(e.bookTicks[mkt], algorithms.BookTick{
			TS:      ts,
			Bid:     bid.Float64,
			Ask:     ask.Float64,
			BidSize: bidSz.Float64,
			AskSize: askSz.Float64,
		})
	}
	if err := bookRows.Err(); err != nil {
		return fmt.Errorf("iterate book rows: %w", err)
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
