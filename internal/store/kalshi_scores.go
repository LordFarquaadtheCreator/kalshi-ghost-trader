package store

import (
	"context"
	"fmt"
)

// UpsertKalshiScore inserts or replaces a Kalshi live score snapshot for an event.
// Called by the kalshilivedata poller on every poll cycle.
func (d *DB) UpsertKalshiScore(ctx context.Context, s KalshiScore) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO kalshi_scores
		(event_ticker, milestone_id, status, sets_home, sets_away,
		 games_home, games_away, points_home, points_away, server,
		 completed_rounds, updated_ts, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_ticker) DO UPDATE SET
			milestone_id = excluded.milestone_id,
			status = excluded.status,
			sets_home = excluded.sets_home,
			sets_away = excluded.sets_away,
			games_home = excluded.games_home,
			games_away = excluded.games_away,
			points_home = excluded.points_home,
			points_away = excluded.points_away,
			server = excluded.server,
			completed_rounds = excluded.completed_rounds,
			updated_ts = excluded.updated_ts,
			payload = excluded.payload`,
		s.EventTicker, s.MilestoneID, s.Status, s.SetsHome, s.SetsAway,
		s.GamesHome, s.GamesAway, s.PointsHome, s.PointsAway, s.Server,
		s.CompletedRounds, s.UpdatedTS, s.Payload)
	if err != nil {
		return fmt.Errorf("upsert kalshi_score: %w", err)
	}
	return nil
}

// GetKalshiScores returns live score snapshots for the given event tickers.
// Used by Engine.LatestScores to fill gaps where API-Tennis has no data.
func (d *DB) GetKalshiScores(ctx context.Context, eventTickers []string) (map[string]KalshiScore, error) {
	if len(eventTickers) == 0 {
		return map[string]KalshiScore{}, nil
	}
	placeholders := ""
	args := make([]any, 0, len(eventTickers))
	for i, et := range eventTickers {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, et)
	}
	query := `SELECT event_ticker, milestone_id, status, sets_home, sets_away,
		games_home, games_away, points_home, points_away, server, completed_rounds,
		updated_ts, payload
		FROM kalshi_scores WHERE event_ticker IN (` + placeholders + `)`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get kalshi_scores: %w", err)
	}
	defer rows.Close()
	result := make(map[string]KalshiScore, len(eventTickers))
	for rows.Next() {
		var s KalshiScore
		if err := rows.Scan(&s.EventTicker, &s.MilestoneID, &s.Status,
			&s.SetsHome, &s.SetsAway, &s.GamesHome, &s.GamesAway,
			&s.PointsHome, &s.PointsAway, &s.Server, &s.CompletedRounds,
			&s.UpdatedTS, &s.Payload); err != nil {
			return nil, fmt.Errorf("scan kalshi_score: %w", err)
		}
		result[s.EventTicker] = s
	}
	return result, nil
}

// HasAPItennisPoints returns true if the points table has any entries
// for the given event_ticker from the API-Tennis scraper (fs_match_id
// is not prefixed with "kalshi-").
func (d *DB) HasAPItennisPoints(ctx context.Context, eventTicker string) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM points WHERE match_ticker = ? AND fs_match_id NOT LIKE 'kalshi-%'`,
		eventTicker).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check apitennis points: %w", err)
	}
	return count > 0, nil
}
