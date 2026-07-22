// Package insights implements the materialized view refresh job.
// REFRESH MATERIALIZED VIEW CONCURRENTLY each view every 5 minutes.
package insights

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// Refresher refreshes all insights materialized views on a schedule.
type Refresher struct {
	db       *gorm.DB
	log      *slog.Logger
	interval time.Duration
	views    []string
}

// NewRefresher creates a refresh job. Default interval is 5 minutes.
func NewRefresher(db *gorm.DB, intervalSecs int, log *slog.Logger) *Refresher {
	if intervalSecs <= 0 {
		intervalSecs = 300
	}
	if log == nil {
		log = slog.Default()
	}
	return &Refresher{
		db:       db,
		log:      log,
		interval: time.Duration(intervalSecs) * time.Second,
		views: []string{
			"insights.strategy_daily",
			"insights.band_performance",
			"insights.match_summary",
			"insights.pool_equity_curve",
		},
	}
}

// Run refreshes all views on a ticker until ctx is cancelled.
func (r *Refresher) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Initial refresh on startup.
	if err := r.refreshAll(ctx); err != nil {
		r.log.Error("insights: initial refresh failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.refreshAll(ctx); err != nil {
				r.log.Error("insights: refresh failed", "err", err)
			}
		}
	}
}

func (r *Refresher) refreshAll(ctx context.Context) error {
	for _, view := range r.views {
		if err := r.refreshView(ctx, view); err != nil {
			return fmt.Errorf("refresh %s: %w", view, err)
		}
	}
	r.log.Debug("insights: all views refreshed", "count", len(r.views))
	return nil
}

func (r *Refresher) refreshView(ctx context.Context, view string) error {
	// Try CONCURRENTLY first; fall back to non-concurrent if the view
	// doesn't have a unique index yet (first refresh after creation).
	stmt := fmt.Sprintf("REFRESH MATERIALIZED VIEW CONCURRENTLY %s", view)
	if err := r.db.WithContext(ctx).Exec(stmt).Error; err != nil {
		r.log.Debug("insights: concurrent refresh failed, falling back", "view", view, "err", err)
		stmt = fmt.Sprintf("REFRESH MATERIALIZED VIEW %s", view)
		return r.db.WithContext(ctx).Exec(stmt).Error
	}
	return nil
}
