// Package schedulechecker polls upcoming markets via REST to detect schedule
// changes. Kalshi can update occurrence_datetime after initial market creation
// (rain delays, scheduling changes). The scanner only runs every 24h, so
// mid-cycle updates are missed — strategies fire on stale schedule data.
//
// The checker queries active markets with occurrence_ts in the future, fetches
// each from REST, updates the DB, and notifies the strategy runtime to refresh
// its cached occurrence_ts. Runs on a short poll interval (default 120s).
//
// Additionally, the checker probes Kalshi's /milestones + /live_data endpoints
// to detect matches that started ahead of their scheduled occurrence_datetime.
// Kalshi's occurrence_datetime is unreliable for ITF matches — it often points
// to a default future slot while the real match is already in progress.
// live_data.details.status is the authoritative live signal (verified across
// multiple samples: "started"/"interrupted" = live, "closed"/"complete" =
// finished). When live_data confirms a match is live, occurrence_ts is moved
// to now so the scheduler tracks immediately.
package schedulechecker

import (
	"context"
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// OccurrenceRefresher is implemented by MultiStrategyRuntime to refresh
// cached occurrence_ts after a schedule update is detected.
type OccurrenceRefresher interface {
	RefreshOccurrenceTS(eventTicker string)
}

// Checker polls upcoming markets via REST and updates stale schedule data.
type Checker struct {
	client    *kalshiclient.Client
	db        *store.DB
	refresher OccurrenceRefresher
	log       *slog.Logger
}

// New creates a schedule checker.
func New(client *kalshiclient.Client, db *store.DB, refresher OccurrenceRefresher, log *slog.Logger) *Checker {
	return &Checker{
		client:    client,
		db:        db,
		refresher: refresher,
		log:       log,
	}
}

// Run polls upcoming markets. Interval is read from config.Cfg.ScheduleCheckerIntervalSecs.
// Blocks until ctx cancelled.
func (c *Checker) Run(ctx context.Context) error {
	interval := time.Duration(config.Cfg.ScheduleCheckerIntervalSecs) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.check(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.check(ctx)
		}
	}
}

// check fetches upcoming markets from DB, REST-fetches each, updates DB on
// schedule changes, and refreshes the strategy runtime cache. Also probes
// live_data to detect matches that started ahead of schedule.
func (c *Checker) check(ctx context.Context) {
	markets, err := c.db.GetUpcomingMarkets(ctx)
	if err != nil {
		c.log.Error("schedule checker: get upcoming markets", "err", err)
		return
	}
	if len(markets) == 0 {
		return
	}

	// Deduplicate by event_ticker — both markets in an event share occurrence_ts
	seen := make(map[string]bool, len(markets))
	updated := 0
	liveStarted := 0
	for _, m := range markets {
		if ctx.Err() != nil {
			return
		}
		if seen[m.EventTicker] {
			continue
		}
		seen[m.EventTicker] = true

		mkt, err := c.client.GetMarket(ctx, m.MarketTicker)
		if err != nil {
			c.log.Warn("schedule checker: fetch market failed",
				"market", m.MarketTicker, "err", err)
			continue
		}

		newOccTS := kalshiclient.ParseISOTime(mkt.OccurrenceDatetime, c.log)
		if newOccTS != 0 && newOccTS != m.OccurrenceTS {
			// Update DB with fresh schedule data
			_, err = c.db.UpsertMarketCheckNew(ctx, store.Market{
				MarketTicker:     mkt.Ticker,
				EventTicker:      mkt.EventTicker,
				SeriesTicker:     m.SeriesTicker,
				PlayerName:       mkt.YesSubTitle,
				TennisCompetitor: kalshiclient.ParseTennisCompetitor(mkt.CustomStrike),
				Status:           mkt.Status,
				OccurrenceTS:     newOccTS,
				OpenTS:           kalshiclient.ParseISOTime(mkt.OpenTime, c.log),
				CloseTS:          kalshiclient.ParseISOTime(mkt.CloseTime, c.log),
				Result:           mkt.Result,
				SettlementTS:     kalshiclient.ParseISOTime(mkt.SettlementTS, c.log),
				SettlementValue:  mkt.SettlementValueDollars,
			})
			if err != nil {
				c.log.Error("schedule checker: update market failed",
					"market", m.MarketTicker, "err", err)
			} else {
				c.log.Info("schedule updated",
					"event", m.EventTicker,
					"market", m.MarketTicker,
					"old_occurrence", time.UnixMilli(m.OccurrenceTS).Format(time.RFC3339),
					"new_occurrence", time.UnixMilli(newOccTS).Format(time.RFC3339))
				c.refresher.RefreshOccurrenceTS(m.EventTicker)
				updated++
			}
		}

		// Live-detection: probe milestones + live_data even when occurrence_ts
		// matches. Kalshi's occurrence_datetime can be a default future slot
		// while the match is already in progress. live_data.details.status is
		// the authoritative signal. Gated by schedule_checker_live_detection.
		if config.Cfg.ScheduleCheckerLiveDetection && c.checkLive(ctx, m.EventTicker) {
			now := time.Now().UnixMilli()
			rows, err := c.db.UpdateOccurrenceTS(ctx, m.EventTicker, now)
			if err != nil {
				c.log.Error("schedule checker: live-detection occurrence update failed",
					"event", m.EventTicker, "err", err)
				continue
			}
			if rows > 0 {
				c.log.Info("live detected, occurrence_ts moved to now",
					"event", m.EventTicker,
					"old_occurrence", time.UnixMilli(m.OccurrenceTS).Format(time.RFC3339),
					"markets_updated", rows)
				c.refresher.RefreshOccurrenceTS(m.EventTicker)
				liveStarted++
			}
		}
	}

	if updated > 0 || liveStarted > 0 {
		c.log.Info("schedule checker: pass complete",
			"checked", len(seen), "updated", updated, "live_started", liveStarted)
	}
}

// checkLive probes /milestones then /live_data for an event. Returns true if
// live_data confirms the match is currently in progress (status "started" or
// "interrupted"). Returns false on any error, missing milestone, or non-live
// status — fails closed (no false positives).
func (c *Checker) checkLive(ctx context.Context, eventTicker string) bool {
	milestones, err := c.client.GetMilestones(ctx, eventTicker)
	if err != nil {
		c.log.Warn("schedule checker: get milestones failed",
			"event", eventTicker, "err", err)
		return false
	}
	if len(milestones) == 0 {
		return false
	}

	ld, err := c.client.GetLiveData(ctx, milestones[0].ID)
	if err != nil {
		// 404 = no live data yet — match not started. Not an error.
		return false
	}

	switch ld.Details.Status {
	case "started", "interrupted":
		return true
	default:
		return false
	}
}
