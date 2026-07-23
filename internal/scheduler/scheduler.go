// Package scheduler schedules per-match WebSocket tracking based on match start times.
//
// The Scheduler periodically polls the PostgreSQL database for active markets and
// starts tracking each market at occurrence_datetime minus a configurable lead
// time (default: 5 minutes). Markets already tracked or pending are skipped.
// Markets no longer active in the DB (settled/closed/finalized) are unsubscribed.
//
// Each scheduled market gets a lightweight goroutine that waits until the
// calculated start time, then calls tracker.StartMatch. The pending map is
// guarded by a mutex to prevent duplicate scheduling.
//
// No REST client is needed — the scheduler reads exclusively from the DB
// populated by the scanner.
package scheduler

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
)

// pendingEntry tracks a scheduled goroutine so it can be cancelled when the
// schedule changes (occurrence_ts moved earlier or later by schedulechecker).
type pendingEntry struct {
	startAt time.Time
	cancel  context.CancelFunc
}

// Scheduler starts tracking markets at occurrence_datetime - leadMinutes.
// Periodically re-scans for new matches and schedules them.
type Scheduler struct {
	db      *store.DB
	tracker *tracker.Tracker
	log     *slog.Logger

	mu      sync.Mutex
	pending map[string]*pendingEntry // market_ticker -> scheduled entry
}

// New creates a scheduler. leadMinutes and poll interval are read live from
// config.Cfg each pass so dashboard updates take effect without restart.
func New(db *store.DB, tr *tracker.Tracker, log *slog.Logger) *Scheduler {
	return &Scheduler{
		db:      db,
		tracker: tr,
		log:     log,
		pending: make(map[string]*pendingEntry),
	}
}

// Run periodically polls the DB for active markets and schedules tracking
// for those whose occurrence_datetime is approaching. Poll interval is read
// live from config — ticker is recreated when the value changes.
func (s *Scheduler) Run(ctx context.Context) error {
	interval := s.pollInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately
	s.scheduleDue(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Recreate ticker if poll interval changed via dashboard
			if newInterval := s.pollInterval(); newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
			}
			s.scheduleDue(ctx)
		}
	}
}

// pollInterval reads SchedulerPollSecs live from config.
func (s *Scheduler) pollInterval() time.Duration {
	return time.Duration(config.Cfg.SchedulerPollSecs) * time.Second
}

// lead reads TrackLeadMinutes live from config.
func (s *Scheduler) lead() time.Duration {
	return time.Duration(config.Cfg.TrackLeadMinutes) * time.Minute
}

// scheduleDue queries active markets, starts tracking those due soon,
// and stops tracking markets no longer active (settled/closed/finalized).
func (s *Scheduler) scheduleDue(ctx context.Context) {
	markets, err := s.db.GetActiveMarkets(ctx)
	if err != nil {
		s.log.Error("get active markets", "err", err)
		return
	}

	// Events with API-Tennis points — match is actually live (point scored).
	// Used to track early starts immediately instead of waiting for schedule.
	// Kalshi REST returns "active" for any tradeable market, including future
	// matches days away. Without this check, all "active" markets track
	// immediately, spawning pollers that exhaust the rate limiter.
	eventsWithPoints, err := s.db.GetMatchTickersWithPoints(ctx)
	if err != nil {
		s.log.Error("get events with points", "err", err)
		return
	}
	liveEvents := make(map[string]bool, len(eventsWithPoints))
	for _, e := range eventsWithPoints {
		liveEvents[e] = true
	}

	now := time.Now()
	lead := s.lead()
	// Sort by occurrence time so we log in order
	sort.Slice(markets, func(i, j int) bool {
		return markets[i].OccurrenceTS < markets[j].OccurrenceTS
	})

	// Get tracked markets once — O(n) not O(n²)
	active := s.tracker.ActiveMarkets()
	trackingSet := make(map[string]bool, len(active))
	for _, a := range active {
		trackingSet[a] = true
	}

	// Build active market set from DB for stop-tracking check
	activeMarketSet := make(map[string]bool, len(markets))
	for _, m := range markets {
		activeMarketSet[m.MarketTicker] = true
	}

	// Stop tracking markets no longer active in DB (settled/closed/finalized)
	stopped := 0
	for _, tracked := range active {
		if !activeMarketSet[tracked] {
			s.log.Info("market no longer active, stopping", "market", tracked)
			s.tracker.StopMatch(tracked)
			stopped++
		}
	}

	scheduled := 0
	for _, m := range markets {
		if m.OccurrenceTS == 0 {
			continue
		}

		occurrence := time.UnixMilli(m.OccurrenceTS)
		startAt := occurrence.Add(-lead)

		// Already tracking?
		if trackingSet[m.MarketTicker] {
			continue
		}

		// Already pending?
		s.mu.Lock()
		entry, pending := s.pending[m.MarketTicker]
		s.mu.Unlock()
		if pending {
			// Schedule checker may have moved occurrence_ts (rain delay resolved,
			// match moved up, or live-detection moved it to now). Re-evaluate
			// against the fresh DB value.
			freshStart := time.UnixMilli(m.OccurrenceTS).Add(-lead)
			if now.After(freshStart) || liveEvents[m.EventTicker] {
				// Due now — cancel the waiting goroutine and track immediately.
				entry.cancel()
				s.mu.Lock()
				delete(s.pending, m.MarketTicker)
				s.mu.Unlock()
				s.startTracking(ctx, m.MarketTicker, m.EventTicker)
			} else if !freshStart.Equal(entry.startAt) {
				// Schedule shifted (earlier or later) but still in the future.
				// Cancel the stale goroutine and reschedule with the new time.
				entry.cancel()
				s.mu.Lock()
				delete(s.pending, m.MarketTicker)
				s.mu.Unlock()
				s.scheduleLater(ctx, m.MarketTicker, m.EventTicker, freshStart)
				scheduled++
				s.log.Info("rescheduled match", "market", m.MarketTicker,
					"old_start", entry.startAt.Format(time.RFC3339),
					"new_start", freshStart.Format(time.RFC3339))
			}
			continue
		}

		// Track immediately if:
		//   - Scheduled start time has passed (occurrence_ts - lead), OR
		//   - API-Tennis has recorded a point for this event (match is live
		//     now, may have started ahead of schedule)
		// Kalshi REST "active" alone is NOT sufficient — it means "tradeable",
		// not "match is happening". Future markets are "active" on REST for
		// days before the match.
		if now.After(startAt) || liveEvents[m.EventTicker] {
			s.startTracking(ctx, m.MarketTicker, m.EventTicker)
			scheduled++
		} else {
			s.scheduleLater(ctx, m.MarketTicker, m.EventTicker, startAt)
			scheduled++
			s.log.Info("scheduled match", "market", m.MarketTicker, "start_at", startAt.Format(time.RFC3339))
		}
	}

	if scheduled > 0 || stopped > 0 {
		s.log.Info("scheduling pass complete", "scheduled", scheduled, "stopped", stopped, "active_markets", len(markets))
	}
}

// scheduleLater spawns a goroutine that waits until startAt, then tracks the
// market. Registers a cancellable entry in the pending map so scheduleDue can
// cancel it if the schedule changes before it fires.
func (s *Scheduler) scheduleLater(ctx context.Context, market, eventTicker string, startAt time.Time) {
	goroutineCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.pending[market] = &pendingEntry{startAt: startAt, cancel: cancel}
	s.mu.Unlock()
	go s.scheduleOne(goroutineCtx, market, eventTicker, startAt)
}

// scheduleOne waits until the scheduled time, then starts tracking. Verifies
// the market is still active in the DB before subscribing — a market can be
// settled/closed (walkover, retirement) between scheduling and fire time.
// On cancel (schedule shifted or ctx cancelled) the pending entry is removed.
func (s *Scheduler) scheduleOne(ctx context.Context, market, eventTicker string, startAt time.Time) {
	wait := time.Until(startAt)
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.mu.Lock()
			// Only delete if this entry still owns the slot. A reschedule
			// already replaced it; deleting the new entry would leak the
			// newer goroutine.
			if e, ok := s.pending[market]; ok && e.startAt.Equal(startAt) {
				delete(s.pending, market)
			}
			s.mu.Unlock()
			return
		case <-timer.C:
		}
	}

	s.mu.Lock()
	delete(s.pending, market)
	s.mu.Unlock()

	// Verify market is still active before subscribing. A walkover or
	// retirement between scheduling and fire time leaves the market
	// settled/closed — subscribing would waste a WS slot + spawn a
	// kalshilivedata poller that never resolves.
	m, err := s.db.GetMarket(ctx, market)
	if err != nil {
		s.log.Warn("scheduleOne: market lookup failed", "market", market, "err", err)
		return
	}
	switch m.Status {
	case "open", "active", "determined":
		// still trackable
	default:
		s.log.Info("scheduleOne: market no longer active, skipping", "market", market, "status", m.Status)
		return
	}

	s.startTracking(ctx, market, eventTicker)
}

// startTracking subscribes to the market via the tracker.
func (s *Scheduler) startTracking(ctx context.Context, market, eventTicker string) {
	if err := s.tracker.StartMatch(ctx, market, eventTicker); err != nil {
		s.log.Error("start tracking", "market", market, "err", err)
		return
	}
	s.log.Info("now tracking", "market", market, "event", eventTicker)
}
