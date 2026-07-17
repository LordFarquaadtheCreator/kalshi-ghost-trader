// Package schedulechecker polls upcoming matches via REST to detect schedule
// changes. Kalshi can update occurrence_datetime after initial market creation
// (rain delays, scheduling changes). The scanner only runs every 24h, so
// mid-cycle updates are missed — strategies fire on stale schedule data.
//
// The checker queries active markets with occurrence_ts in the future, fetches
// each from REST, updates the DB, and notifies the strategy runtime to refresh
// its cached occurrence_ts. Runs on a short poll interval (default 120s).
package schedulechecker

import (
	"context"
	"log/slog"
	"time"

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
	client   *kalshiclient.Client
	db       *store.DB
	refresher OccurrenceRefresher
	log      *slog.Logger
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

// Run polls upcoming markets at the given interval. Blocks until ctx cancelled.
func (c *Checker) Run(ctx context.Context, interval time.Duration) error {
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
// schedule changes, and refreshes the strategy runtime cache.
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
		if newOccTS == 0 || newOccTS == m.OccurrenceTS {
			continue
		}

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
			continue
		}

		c.log.Info("schedule updated",
			"event", m.EventTicker,
			"market", m.MarketTicker,
			"old_occurrence", time.UnixMilli(m.OccurrenceTS).Format(time.RFC3339),
			"new_occurrence", time.UnixMilli(newOccTS).Format(time.RFC3339))

		c.refresher.RefreshOccurrenceTS(m.EventTicker)
		updated++
	}

	if updated > 0 {
		c.log.Info("schedule checker: pass complete",
			"checked", len(seen), "updated", updated)
	}
}
