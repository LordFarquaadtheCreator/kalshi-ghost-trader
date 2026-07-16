// Package scheduler schedules per-match WebSocket tracking based on match start times.
//
// The Scheduler periodically polls the SQLite database for active markets and
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

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
)

// Scheduler starts tracking markets at occurrence_datetime - leadMinutes.
// Periodically re-scans for new matches and schedules them.
type Scheduler struct {
	db      *store.DB
	tracker *tracker.Tracker
	lead    time.Duration
	log     *slog.Logger

	mu      sync.Mutex
	pending map[string]time.Time // market_ticker -> scheduled start time
}

// New creates a scheduler.
func New(db *store.DB, tr *tracker.Tracker, leadMinutes int, log *slog.Logger) *Scheduler {
	return &Scheduler{
		db:      db,
		tracker: tr,
		lead:    time.Duration(leadMinutes) * time.Minute,
		log:     log,
		pending: make(map[string]time.Time),
	}
}

// Run periodically polls the DB for active markets and schedules tracking
// for those whose occurrence_datetime is approaching.
func (s *Scheduler) Run(ctx context.Context, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Run immediately
	s.scheduleDue(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.scheduleDue(ctx)
		}
	}
}

// scheduleDue queries active markets, starts tracking those due soon,
// and stops tracking markets no longer active (settled/closed/finalized).
func (s *Scheduler) scheduleDue(ctx context.Context) {
	markets, err := s.db.GetActiveMarkets(ctx)
	if err != nil {
		s.log.Error("get active markets", "err", err)
		return
	}

	now := time.Now()
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
		startAt := occurrence.Add(-s.lead)

		// Already tracking?
		if trackingSet[m.MarketTicker] {
			continue
		}

		// Already pending?
		s.mu.Lock()
		_, pending := s.pending[m.MarketTicker]
		s.mu.Unlock()
		if pending {
			continue
		}

		// If occurrence is in the past or within lead window, start now
		if now.After(startAt) {
			s.startTracking(ctx, m.MarketTicker, m.EventTicker)
			scheduled++
		} else {
			// Schedule a goroutine that waits until startAt
			s.mu.Lock()
			s.pending[m.MarketTicker] = startAt
			s.mu.Unlock()

			go s.scheduleOne(ctx, m.MarketTicker, m.EventTicker, startAt)
			scheduled++
			s.log.Info("scheduled match", "market", m.MarketTicker, "start_at", startAt.Format(time.RFC3339))
		}
	}

	if scheduled > 0 || stopped > 0 {
		s.log.Info("scheduling pass complete", "scheduled", scheduled, "stopped", stopped, "active_markets", len(markets))
	}
}

// scheduleOne waits until the scheduled time, then starts tracking.
func (s *Scheduler) scheduleOne(ctx context.Context, market, eventTicker string, startAt time.Time) {
	wait := time.Until(startAt)
	if wait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	s.mu.Lock()
	delete(s.pending, market)
	s.mu.Unlock()

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
