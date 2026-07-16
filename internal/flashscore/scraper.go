package flashscore

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// StartPolling adds an event ticker to the active polling set.
// Called by tracker when the first market for an event is subscribed.
// Markets are ordered [home, away] by matching FlashScore player names
// to Kalshi market player_name fields.
func (s *Scraper) StartPolling(eventTicker string) {
	s.activeMu.Lock()
	s.active[eventTicker] = struct{}{}
	s.activeMu.Unlock()
	s.log.Info("flashscore polling started", "event", eventTicker)

	if s.strategy != nil {
		markets, err := s.db.GetMarketsByEvent(context.Background(), eventTicker)
		if err != nil {
			s.log.Error("strategy: get markets for registration", "event", eventTicker, "err", err)
			return
		}
		tickers := orderMarketsByFlashScore(s, eventTicker, markets)
		s.strategy.RegisterMarkets(eventTicker, tickers)
	}
}

// orderMarketsByFlashScore returns market tickers ordered [home, away]
// by matching FlashScore player names to Kalshi market player_name.
// Falls back to DB order if matching fails.
func orderMarketsByFlashScore(s *Scraper, eventTicker string, markets []store.Market) []string {
	if len(markets) < 2 {
		var tickers []string
		for _, m := range markets {
			tickers = append(tickers, m.MarketTicker)
		}
		return tickers
	}

	fsMatches, err := s.db.GetFSMatchesByEvent(context.Background(), eventTicker)
	if err != nil || len(fsMatches) == 0 {
		// No FS mapping — use DB order
		return []string{markets[0].MarketTicker, markets[1].MarketTicker}
	}

	fsm := fsMatches[0]
	homeLN := normalizeLastName(extractLastName(fsm.HomePlayer))
	awayLN := normalizeLastName(extractLastName(fsm.AwayPlayer))

	var homeTicker, awayTicker string
	for _, m := range markets {
		mktLN := normalizeLastName(extractLastName(m.PlayerName))
		if mktLN == homeLN && homeTicker == "" {
			homeTicker = m.MarketTicker
		} else if mktLN == awayLN && awayTicker == "" {
			awayTicker = m.MarketTicker
		}
	}

	if homeTicker != "" && awayTicker != "" {
		return []string{homeTicker, awayTicker}
	}
	// Fallback: DB order
	return []string{markets[0].MarketTicker, markets[1].MarketTicker}
}

// StopPolling removes an event ticker from the active polling set.
// Called by tracker when the last market for an event is unsubscribed.
func (s *Scraper) StopPolling(eventTicker string) {
	s.activeMu.Lock()
	delete(s.active, eventTicker)
	s.activeMu.Unlock()

	if s.strategy != nil {
		s.strategy.UnregisterMarkets(eventTicker)
	}

	s.log.Info("flashscore polling stopped", "event", eventTicker)
}

// Scraper polls FlashScore for tennis match data and point-by-point scores.
// Two loops run concurrently:
//   - Feed scanner: polls daily feed, stores new matches, maps to Kalshi events
//   - Point poller: polls active matches for point-by-point data, ingests new points
type Scraper struct {
	client        *Client
	db            *store.DB
	tickWriter    *store.TickWriter
	strategy      algorithms.Strategy // nil if signal disabled
	log           *slog.Logger
	scanInterval  time.Duration
	pollInterval  time.Duration
	lookaheadDays int

	activeMu sync.Mutex
	active   map[string]struct{} // event tickers currently being polled
}

// New creates a FlashScore scraper.
func New(db *store.DB, tw *store.TickWriter, strat algorithms.Strategy, scanInterval, pollInterval time.Duration,
	lookaheadDays int, log *slog.Logger) *Scraper {
	return &Scraper{
		client:        NewClient(15 * time.Second),
		db:            db,
		tickWriter:    tw,
		strategy:      strat,
		log:           log,
		scanInterval:  scanInterval,
		pollInterval:  pollInterval,
		lookaheadDays: lookaheadDays,
		active:        make(map[string]struct{}),
	}
}

const pollConcurrency = 8

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

// pollActiveMatches fetches point-by-point data for events in the active set.
// Only polls matches for events the tracker is subscribed to via StartPolling.
// Detects new points by comparing against previously stored point count.
// Polls concurrently with a bounded worker pool to avoid stalling on
// slow/throttled responses.
func (s *Scraper) pollActiveMatches(ctx context.Context) error {
	// Snapshot active event tickers
	s.activeMu.Lock()
	events := make([]string, 0, len(s.active))
	for ev := range s.active {
		events = append(events, ev)
	}
	s.activeMu.Unlock()
	if len(events) == 0 {
		return nil
	}

	// Gather FS matches for all active events
	var active []store.FSMatch
	for _, ev := range events {
		matches, err := s.db.GetFSMatchesByEvent(ctx, ev)
		if err != nil {
			s.log.Error("get fs matches by event", "event", ev, "err", err)
			continue
		}
		active = append(active, matches...)
	}
	if len(active) == 0 {
		return nil
	}

	sem := make(chan struct{}, pollConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	totalNew := 0

	for i := range active {
		fsm := active[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			newPts, status, err := s.pollMatchPoints(ctx, fsm)
			if err != nil {
				s.log.Error("poll match points", "fs_id", fsm.FSMatchID, "err", err)
				return
			}
			mu.Lock()
			totalNew += newPts
			mu.Unlock()

			_ = s.db.UpdateFSMatchPolled(ctx, fsm.FSMatchID, status)
		}()
	}
	wg.Wait()

	if totalNew > 0 {
		s.log.Info("flashscore poll: new points", "matches", len(active), "points", totalNew)
	}
	return nil
}

// pollMatchPoints fetches point-by-point data for one match, diffs against
// stored points, and ingests new ones. Refreshes fs_status from dc_1.
// Returns count of new points and the refreshed status.
func (s *Scraper) pollMatchPoints(ctx context.Context, fsm store.FSMatch) (int, int, error) {
	// Refresh status from dc_1 — DB status may be stale
	status := fsm.FSStatus
	if info, err := s.client.FetchMatchInfo(ctx, fsm.FSMatchID); err == nil && info != "" {
		if updated := ParseMatchStatus(info); updated > 0 {
			status = updated
		}
	}
	// Skip point polling for finished matches
	if status == 1 {
		return 0, status, nil
	}

	feed, err := s.fetchPointsWithRetry(ctx, fsm.FSMatchID)
	if err != nil {
		return 0, status, err
	}
	if feed == "" {
		return 0, status, nil
	}

	mp := ParsePointByPoint(feed, fsm.FSMatchID)
	if len(mp.Sets) == 0 {
		return 0, status, nil
	}

	// Count total points already stored for this match
	storedCount, err := s.db.GetPointCount(ctx, fsm.EventTicker)
	if err != nil {
		return 0, status, fmt.Errorf("count stored: %w", err)
	}

	// Flatten all parsed points
	var allPoints []PointData
	for _, set := range mp.Sets {
		allPoints = append(allPoints, set.Points...)
	}

	// Diff: only ingest points beyond what's stored
	newCount := len(allPoints) - storedCount
	if newCount <= 0 {
		return 0, status, nil
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

	if s.strategy != nil {
		s.strategy.OnPoints(pts)
	}

	return newCount, status, nil
}

// fetchPointsWithRetry fetches df_mh_1 with up to 2 retries on timeout.
// Backoff: 1s, 2s. Stalled connections fail at ResponseHeaderTimeout (5s).
func (s *Scraper) fetchPointsWithRetry(ctx context.Context, matchID string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		feed, err := s.client.FetchPointByPoint(ctx, matchID)
		if err == nil {
			return feed, nil
		}
		lastErr = err
	}
	return "", lastErr
}
