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
