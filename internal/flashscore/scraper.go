package flashscore

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Scraper polls FlashScore for tennis match data and point-by-point scores.
// Two loops run concurrently:
//   - Feed scanner: polls daily feed, stores new matches, maps to Kalshi events
//   - Point poller: polls active matches for point-by-point data, ingests new points
type Scraper struct {
	client       *Client
	db           *store.DB
	tickWriter   *store.TickWriter
	log          *slog.Logger
	scanInterval time.Duration
	pollInterval time.Duration
	lookaheadDays int
}

// New creates a FlashScore scraper.
func New(db *store.DB, tw *store.TickWriter, scanInterval, pollInterval time.Duration,
	lookaheadDays int, log *slog.Logger) *Scraper {
	return &Scraper{
		client:        NewClient(30 * time.Second),
		db:            db,
		tickWriter:    tw,
		log:           log,
		scanInterval:  scanInterval,
		pollInterval:  pollInterval,
		lookaheadDays: lookaheadDays,
	}
}

// Run starts the scraper. Blocks until ctx cancelled.
func (s *Scraper) Run(ctx context.Context) error {
	s.log.Info("flashscore scraper starting",
		"scan_interval", s.scanInterval, "poll_interval", s.pollInterval)

	// Run initial feed scan immediately
	if err := s.scanFeed(ctx); err != nil {
		s.log.Error("flashscore initial scan failed", "err", err)
	}

	scanTicker := time.NewTicker(s.scanInterval)
	defer scanTicker.Stop()

	pollTicker := time.NewTicker(s.pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-scanTicker.C:
			if err := s.scanFeed(ctx); err != nil {
				s.log.Error("flashscore scan failed", "err", err)
			}

		case <-pollTicker.C:
			if err := s.pollActiveMatches(ctx); err != nil {
				s.log.Error("flashscore poll failed", "err", err)
			}
		}
	}
}

// scanFeed fetches the daily feed for today + lookahead days, stores new
// matches, and attempts to map unmapped matches to Kalshi events.
func (s *Scraper) scanFeed(ctx context.Context) error {
	var allMatches []FeedMatch
	// day -1 = today, 0 = tomorrow, 1 = day after
	for day := -1; day <= s.lookaheadDays; day++ {
		feed, err := s.client.FetchDailyFeed(ctx, day)
		if err != nil {
			s.log.Error("fetch daily feed", "day", day, "err", err)
			continue
		}
		if feed == "" {
			continue
		}
		matches := ParseDailyFeed(feed)
		allMatches = append(allMatches, matches...)
	}

	if len(allMatches) == 0 {
		s.log.Info("flashscore scan: no matches found")
		return nil
	}

	// Store all matches in flashscore_matches table
	newCount := 0
	for _, m := range allMatches {
		fsm := store.FSMatch{
			FSMatchID:  m.ID,
			HomePlayer: m.HomeName,
			AwayPlayer: m.AwayName,
			Tournament: m.Tournament,
			Surface:    m.Surface,
			Category:   m.Category,
			StartTS:    m.StartTS,
			FSStatus:   m.StageType,
		}
		// Check if new by trying to get first
		_, err := s.db.GetFSMatch(ctx, m.ID)
		if err != nil {
			// New match
			newCount++
		}
		if err := s.db.UpsertFSMatch(ctx, fsm); err != nil {
			s.log.Error("upsert fs match", "id", m.ID, "err", err)
		}
	}

	// Try to map unmapped matches to Kalshi events
	if err := s.mapToKalshiEvents(ctx); err != nil {
		s.log.Error("map fs matches to kalshi", "err", err)
	}

	s.log.Info("flashscore scan complete",
		"total", len(allMatches), "new", newCount)
	return nil
}

// mapToKalshiEvents finds unmapped FlashScore matches and tries to link them
// to Kalshi events by player name matching.
func (s *Scraper) mapToKalshiEvents(ctx context.Context) error {
	unmapped, err := s.db.GetUnmappedFSMatches(ctx)
	if err != nil {
		return fmt.Errorf("get unmapped: %w", err)
	}
	if len(unmapped) == 0 {
		return nil
	}

	// Get all Kalshi events — could be large, but tennis events are bounded
	// by active scan window. For efficiency, only get events from last 7 days.
	// The store doesn't have a time-filtered event query, so we get all.
	// In practice the scanner only stores recent events.
	rows, err := s.db.GetAllEventsForMatching(ctx)
	if err != nil {
		return fmt.Errorf("get kalshi events: %w", err)
	}

	mappings := MatchEventsToFSMatches(rows, unmapped)
	mapped := 0
	for fsID, eventTicker := range mappings {
		if err := s.db.MapFSMatchToEvent(ctx, fsID, eventTicker); err != nil {
			s.log.Error("map fs match", "fs_id", fsID, "event", eventTicker, "err", err)
			continue
		}
		mapped++
	}
	if mapped > 0 {
		s.log.Info("mapped fs matches to kalshi events", "count", mapped)
	}
	return nil
}

// pollActiveMatches fetches point-by-point data for in-progress matches.
// Only polls matches that are mapped to Kalshi events (have event_ticker).
// Detects new points by comparing against previously stored point count.
func (s *Scraper) pollActiveMatches(ctx context.Context) error {
	active, err := s.db.GetActiveFSMatches(ctx)
	if err != nil {
		return fmt.Errorf("get active fs matches: %w", err)
	}
	if len(active) == 0 {
		return nil
	}

	totalNew := 0
	for _, fsm := range active {
		newPts, err := s.pollMatchPoints(ctx, fsm)
		if err != nil {
			s.log.Error("poll match points", "fs_id", fsm.FSMatchID, "err", err)
			continue
		}
		totalNew += newPts

		// Update polled timestamp + status
		_ = s.db.UpdateFSMatchPolled(ctx, fsm.FSMatchID, fsm.FSStatus)
	}

	if totalNew > 0 {
		s.log.Info("flashscore poll: new points", "matches", len(active), "points", totalNew)
	}
	return nil
}

// pollMatchPoints fetches point-by-point data for one match, diffs against
// stored points, and ingests new ones. Returns count of new points.
func (s *Scraper) pollMatchPoints(ctx context.Context, fsm store.FSMatch) (int, error) {
	feed, err := s.client.FetchPointByPoint(ctx, fsm.FSMatchID)
	if err != nil {
		return 0, err
	}
	if feed == "" {
		return 0, nil
	}

	mp := ParsePointByPoint(feed, fsm.FSMatchID)
	if len(mp.Sets) == 0 {
		return 0, nil
	}

	// Count total points already stored for this match
	storedCount, err := s.db.GetPointCount(ctx, fsm.EventTicker)
	if err != nil {
		return 0, fmt.Errorf("count stored: %w", err)
	}

	// Flatten all parsed points
	var allPoints []PointData
	for _, set := range mp.Sets {
		allPoints = append(allPoints, set.Points...)
	}

	// Diff: only ingest points beyond what's stored
	newCount := len(allPoints) - storedCount
	if newCount <= 0 {
		return 0, nil
	}

	// Take only the new points
	startIdx := storedCount
	if startIdx > len(allPoints) {
		startIdx = len(allPoints)
	}
	newPoints := allPoints[startIdx:]

	// Convert to store.Point and ingest
	now := time.Now().UnixMilli()
	var pts []store.Point
	for _, pd := range newPoints {
		pts = append(pts, store.Point{
			MatchTicker:  fsm.EventTicker,
			FSMatchID:    fsm.FSMatchID,
			TsMs:         now, // live: use recv time as ts_ms
			RecvTS:       now,
			SetNumber:    pd.SetNumber,
			GameNumber:   pd.GameNumber,
			PointNumber:  pd.PointNumber,
			Server:       pd.Server,
			Scorer:       pd.Scorer,
			HomePoints:   pd.HomePoints,
			AwayPoints:   pd.AwayPoints,
			HomeGames:    pd.HomeGames,
			AwayGames:    pd.AwayGames,
			IsTiebreak:   pd.IsTiebreak,
			IsBreakPoint: pd.IsBreakPoint,
			Payload:      pd.RawHL,
		})
	}

	s.tickWriter.IngestPoints(pts)
	return newCount, nil
}
