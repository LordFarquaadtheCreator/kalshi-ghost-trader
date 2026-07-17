package store

import (
	"context"
	"fmt"
)

// GetPointsByMatch returns all point-by-point entries for a match, ordered by time.
func (d *DB) GetPointsByMatch(ctx context.Context, matchTicker string) ([]Point, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0), COALESCE(away_set_games, 0),
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM points
		WHERE match_ticker = ?
		ORDER BY ts_ms ASC, set_number ASC, game_number ASC, point_number ASC
	`, matchTicker)
	if err != nil {
		return nil, fmt.Errorf("get points: %w", err)
	}
	defer rows.Close()

	var points []Point
	for rows.Next() {
		var p Point
		var isTB, isBP, isSP, isMP int
		if err := rows.Scan(
			&p.TS, &p.SetNumber, &p.GameNumber, &p.PointNumber,
			&p.Server, &p.Scorer, &p.HomePoints, &p.AwayPoints,
			&p.HomeGames, &p.AwayGames,
			&p.HomeSetGames, &p.AwaySetGames,
			&isTB, &isBP, &isSP, &isMP,
		); err != nil {
			return nil, fmt.Errorf("scan point: %w", err)
		}
		p.IsTiebreak = isTB != 0
		p.IsBreakPoint = isBP != 0
		p.IsSetPoint = isSP != 0
		p.IsMatchPoint = isMP != 0
		points = append(points, p)
	}
	return points, rows.Err()
}

// GetMatchTickersWithPoints returns event tickers that have point data.
func (d *DB) GetMatchTickersWithPoints(ctx context.Context) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT match_ticker FROM points WHERE ts_ms IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("get match tickers with points: %w", err)
	}
	defer rows.Close()

	var tickers []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tickers = append(tickers, t)
	}
	return tickers, rows.Err()
}

// InsertPointBatch inserts a batch of point entries. Uses INSERT OR IGNORE
// to deduplicate on (match_ticker, set_number, game_number, point_number).
func (d *DB) InsertPointBatch(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin point batch: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO points
			(match_ticker, fs_match_id, ts_ms, recv_ts,
			 set_number, game_number, point_number,
			 server, scorer, home_points, away_points,
			 home_games, away_games, home_set_games, away_set_games,
			 is_tiebreak, is_break_point, is_set_point, is_match_point)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare point insert: %w", err)
	}
	defer stmt.Close()

	for _, p := range points {
		var isTB, isBP, isSP, isMP int
		if p.IsTiebreak {
			isTB = 1
		}
		if p.IsBreakPoint {
			isBP = 1
		}
		if p.IsSetPoint {
			isSP = 1
		}
		if p.IsMatchPoint {
			isMP = 1
		}
		if _, err := stmt.ExecContext(ctx,
			p.MatchTicker, p.FSMatchID, p.TS, p.RecvTS,
			p.SetNumber, p.GameNumber, p.PointNumber,
			p.Server, p.Scorer, p.HomePoints, p.AwayPoints,
			p.HomeGames, p.AwayGames, p.HomeSetGames, p.AwaySetGames,
			isTB, isBP, isSP, isMP,
		); err != nil {
			return fmt.Errorf("insert point: %w", err)
		}
	}
	return tx.Commit()
}

// UpdatePointFlags sets is_set_point and is_match_point for existing rows.
// Used by backfill to fix historical data that was inserted without flags.
func (d *DB) UpdatePointFlags(ctx context.Context, matchTicker string, setNumber, gameNumber, pointNumber int, isSetPoint, isMatchPoint bool) error {
	var sp, mp int
	if isSetPoint {
		sp = 1
	}
	if isMatchPoint {
		mp = 1
	}
	_, err := d.db.ExecContext(ctx, `
		UPDATE points SET is_set_point = ?, is_match_point = ?
		WHERE match_ticker = ? AND set_number = ? AND game_number = ? AND point_number = ?
	`, sp, mp, matchTicker, setNumber, gameNumber, pointNumber)
	if err != nil {
		return fmt.Errorf("update point flags: %w", err)
	}
	return nil
}

// GetAllPoints returns all points ordered by match and time. Used for backfill.
func (d *DB) GetAllPoints(ctx context.Context) ([]Point, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT match_ticker, COALESCE(fs_match_id, ''), ts_ms, COALESCE(recv_ts, 0),
		       set_number, game_number, point_number,
		       server, scorer, home_points, away_points,
		       home_games, away_games,
		       COALESCE(home_set_games, 0), COALESCE(away_set_games, 0),
		       is_tiebreak, is_break_point, is_set_point, is_match_point
		FROM points
		ORDER BY match_ticker, set_number, game_number, point_number
	`)
	if err != nil {
		return nil, fmt.Errorf("get all points: %w", err)
	}
	defer rows.Close()

	var points []Point
	for rows.Next() {
		var p Point
		var isTB, isBP, isSP, isMP int
		if err := rows.Scan(
			&p.MatchTicker, &p.FSMatchID, &p.TS, &p.RecvTS,
			&p.SetNumber, &p.GameNumber, &p.PointNumber,
			&p.Server, &p.Scorer, &p.HomePoints, &p.AwayPoints,
			&p.HomeGames, &p.AwayGames,
			&p.HomeSetGames, &p.AwaySetGames,
			&isTB, &isBP, &isSP, &isMP,
		); err != nil {
			return nil, fmt.Errorf("scan point: %w", err)
		}
		p.IsTiebreak = isTB != 0
		p.IsBreakPoint = isBP != 0
		p.IsSetPoint = isSP != 0
		p.IsMatchPoint = isMP != 0
		points = append(points, p)
	}
	return points, rows.Err()
}
