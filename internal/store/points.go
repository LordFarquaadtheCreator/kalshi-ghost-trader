package store

import (
	"context"
	"database/sql"
)

// InsertPointsBatch inserts a batch of points in one transaction.
func (d *DB) InsertPointsBatch(ctx context.Context, pts []Point) error {
	if len(pts) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO points (match_ticker, fs_match_id, ts_ms, recv_ts,
    set_number, game_number, point_number, server, scorer,
    home_points, away_points, home_games, away_games,
    home_set_games, away_set_games, is_tiebreak, is_break_point,
    is_match_point, is_set_point, payload)
VALUES (?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range pts {
		var tsMs interface{}
		if p.TsMs > 0 {
			tsMs = p.TsMs
		}
		var homeSetGames, awaySetGames interface{}
		if p.HomeSetGames > 0 {
			homeSetGames = p.HomeSetGames
		}
		if p.AwaySetGames > 0 {
			awaySetGames = p.AwaySetGames
		}
		var tb, bp, mp, sp int
		if p.IsTiebreak {
			tb = 1
		}
		if p.IsBreakPoint {
			bp = 1
		}
		if p.IsMatchPoint {
			mp = 1
		}
		if p.IsSetPoint {
			sp = 1
		}
		if _, err := stmt.ExecContext(ctx,
			p.MatchTicker, p.FSMatchID, tsMs, p.RecvTS,
			p.SetNumber, p.GameNumber, p.PointNumber, p.Server, p.Scorer,
			p.HomePoints, p.AwayPoints, p.HomeGames, p.AwayGames,
			homeSetGames, awaySetGames, tb, bp, mp, sp, p.Payload,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetPointCount returns total points stored for a match.
func (d *DB) GetPointCount(ctx context.Context, matchTicker string) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM points WHERE match_ticker = ?`, matchTicker).Scan(&n)
	return n, err
}

// GetPointsByMatch returns all points for a match, ordered by ts_ms.
func (d *DB) GetPointsByMatch(ctx context.Context, matchTicker string) ([]Point, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT match_ticker, fs_match_id, ts_ms, recv_ts,
    set_number, game_number, point_number, server, scorer,
    home_points, away_points, home_games, away_games,
    home_set_games, away_set_games, is_tiebreak, is_break_point,
    is_match_point, is_set_point, payload
FROM points WHERE match_ticker = ? AND ts_ms IS NOT NULL
ORDER BY ts_ms`, matchTicker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pts []Point
	for rows.Next() {
		var p Point
		var tsMs, recvTS, homeSetGames, awaySetGames sql.NullInt64
		var isTB, isBP, isMP, isSP int
		var payload string
		if err := rows.Scan(&p.MatchTicker, &p.FSMatchID, &tsMs, &recvTS,
			&p.SetNumber, &p.GameNumber, &p.PointNumber, &p.Server, &p.Scorer,
			&p.HomePoints, &p.AwayPoints, &p.HomeGames, &p.AwayGames,
			&homeSetGames, &awaySetGames, &isTB, &isBP, &isMP, &isSP, &payload); err != nil {
			return nil, err
		}
		p.TsMs = tsMs.Int64
		p.RecvTS = recvTS.Int64
		p.HomeSetGames = int(homeSetGames.Int64)
		p.AwaySetGames = int(awaySetGames.Int64)
		p.IsTiebreak = isTB == 1
		p.IsBreakPoint = isBP == 1
		p.IsMatchPoint = isMP == 1
		p.IsSetPoint = isSP == 1
		p.Payload = payload
		pts = append(pts, p)
	}
	return pts, rows.Err()
}

// GetSettledMarkets returns recently settled markets with results, ordered by close_ts desc.
func (d *DB) GetSettledMarkets(ctx context.Context, limit int) ([]Market, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT market_ticker, event_ticker, series_ticker, player_name, status, close_ts, result
FROM markets
WHERE close_ts > 0 AND result != '' AND result != 'scalar'
ORDER BY close_ts DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mkts []Market
	for rows.Next() {
		var m Market
		if err := rows.Scan(&m.MarketTicker, &m.EventTicker, &m.SeriesTicker,
			&m.PlayerName, &m.Status, &m.CloseTS, &m.Result); err != nil {
			return nil, err
		}
		mkts = append(mkts, m)
	}
	return mkts, rows.Err()
}
