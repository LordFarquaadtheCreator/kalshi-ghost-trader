// Package scheduler polls the DB for active markets and starts tracking
// each match at occurrence_datetime minus a configurable lead time.
package scheduler

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/tracker"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/strategy"
	"gorm.io/gorm"
)

// Scheduler starts tracking markets at occurrence_datetime - leadMinutes.
type Scheduler struct {
	db         *gorm.DB
	tracker    *tracker.Tracker
	strategies []strategy.Strategy
	lead       time.Duration
	pollSecs   int
	log        *slog.Logger

	mu      sync.Mutex
	pending map[string]time.Time // event_ticker -> scheduled start time
}

// New creates a scheduler.
func New(db *gorm.DB, tr *tracker.Tracker, strategies []strategy.Strategy, leadMinutes, pollSecs int, log *slog.Logger) *Scheduler {
	if leadMinutes <= 0 {
		leadMinutes = 5
	}
	if pollSecs <= 0 {
		pollSecs = 30
	}
	return &Scheduler{
		db:         db,
		tracker:    tr,
		strategies: strategies,
		lead:       time.Duration(leadMinutes) * time.Minute,
		pollSecs:   pollSecs,
		log:        log,
		pending:    make(map[string]time.Time),
	}
}

// Run polls the DB and schedules match tracking.
func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(s.pollSecs) * time.Second)
	defer ticker.Stop()

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

type activeMarket struct {
	MarketTicker string `gorm:"column:market_ticker"`
	EventTicker  string `gorm:"column:event_ticker"`
	OccurrenceTS int64  `gorm:"column:occurrence_ts"`
	Status       string `gorm:"column:status"`
}

func (activeMarket) TableName() string { return "markets" }

func (s *Scheduler) scheduleDue(ctx context.Context) {
	var markets []activeMarket
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []string{"open", "active", "determined"}).
		Find(&markets).Error; err != nil {
		s.log.Error("scheduler: query active markets", "err", err)
		return
	}

	// Group markets by event ticker.
	events := make(map[string][]string)
	occurrences := make(map[string]int64)
	for _, m := range markets {
		events[m.EventTicker] = append(events[m.EventTicker], m.MarketTicker)
		if occurrences[m.EventTicker] == 0 || m.OccurrenceTS < occurrences[m.EventTicker] {
			occurrences[m.EventTicker] = m.OccurrenceTS
		}
	}

	now := time.Now()
	type eventSchedule struct {
		ticker    string
		markets   []string
		occurs    int64
		startAt   time.Time
	}
	var schedules []eventSchedule
	for ev, mkts := range events {
		occurs := occurrences[ev]
		if occurs == 0 {
			continue
		}
		startAt := time.UnixMilli(occurs).Add(-s.lead)
		schedules = append(schedules, eventSchedule{ev, mkts, occurs, startAt})
	}
	sort.Slice(schedules, func(i, j int) bool { return schedules[i].occurs < schedules[j].occurs })

	// ActiveMatches returns count, not list. We track scheduled events
	// in s.pending to avoid duplicates. The tracker is idempotent —
	// StartMatch on an already-tracked event is a no-op.

	scheduled := 0
	for _, sch := range schedules {
		s.mu.Lock()
		_, pending := s.pending[sch.ticker]
		s.mu.Unlock()
		if pending {
			continue
		}

		if now.After(sch.startAt) {
			s.startTracking(ctx, sch.ticker, sch.markets)
			scheduled++
		} else {
			s.mu.Lock()
			s.pending[sch.ticker] = sch.startAt
			s.mu.Unlock()

			go s.scheduleOne(ctx, sch.ticker, sch.markets, sch.startAt)
			scheduled++
			s.log.Info("scheduler: scheduled match", "event", sch.ticker, "start_at", sch.startAt.Format(time.RFC3339))
		}
	}

	if scheduled > 0 {
		s.log.Info("scheduler: pass complete", "scheduled", scheduled, "active_events", len(schedules))
	}
}

func (s *Scheduler) scheduleOne(ctx context.Context, eventTicker string, markets []string, startAt time.Time) {
	wait := time.Until(startAt)
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.mu.Lock()
			delete(s.pending, eventTicker)
			s.mu.Unlock()
			return
		case <-timer.C:
		}
	}

	s.mu.Lock()
	delete(s.pending, eventTicker)
	s.mu.Unlock()

	s.startTracking(ctx, eventTicker, markets)
}

func (s *Scheduler) startTracking(ctx context.Context, eventTicker string, markets []string) {
	if err := s.tracker.StartMatch(ctx, eventTicker, markets, s.strategies); err != nil {
		s.log.Error("scheduler: start tracking", "event", eventTicker, "err", err)
		return
	}
	s.log.Info("scheduler: now tracking", "event", eventTicker, "markets", len(markets))
}
