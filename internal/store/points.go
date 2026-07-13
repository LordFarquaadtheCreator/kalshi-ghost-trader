package store

import "context"

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
    home_set_games, away_set_games, is_tiebreak, is_break_point, payload)
VALUES (?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?, ?,?,?)`)
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
		var tb, bp int
		if p.IsTiebreak {
			tb = 1
		}
		if p.IsBreakPoint {
			bp = 1
		}
		if _, err := stmt.ExecContext(ctx,
			p.MatchTicker, p.FSMatchID, tsMs, p.RecvTS,
			p.SetNumber, p.GameNumber, p.PointNumber, p.Server, p.Scorer,
			p.HomePoints, p.AwayPoints, p.HomeGames, p.AwayGames,
			homeSetGames, awaySetGames, tb, bp, p.Payload,
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
