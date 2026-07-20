// Package reconciler fills settlement gaps by polling the Kalshi REST API
// for markets that have orders but no result (missed WS settled events).
//
// Runs as a background goroutine alongside the scanner. Unlike the scanner
// (which scans all series every 24h), the reconciler targets only unresolved
// markets — those with orders but empty result, or active markets past their
// close_ts + grace period. Fetches each via GET /markets/{ticker}, updates
// the market row, and runs post-settlement finalization (coverage classification,
// payload pruning) once both markets in an event are finalized.
package reconciler

import (
	"context"
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// closeGraceMS is how long after close_ts before a market is considered
// overdue for settlement. 30 minutes — matches can run long, rain delays, etc.
const closeGraceMS = 30 * 60 * 1000

// Reconciler polls for unresolved markets and fills settlement data from REST.
type Reconciler struct {
	client *kalshiclient.Client
	db     *store.DB
	log    *slog.Logger
}

// New creates a reconciler.
func New(client *kalshiclient.Client, db *store.DB, log *slog.Logger) *Reconciler {
	return &Reconciler{client: client, db: db, log: log}
}

// Run polls for unresolved markets at the given interval. Blocks until ctx cancelled.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

// reconcile queries unresolved markets, fetches each from REST, updates the DB.
func (r *Reconciler) reconcile(ctx context.Context) {
	markets, err := r.db.GetUnresolvedMarkets(ctx, closeGraceMS)
	if err != nil {
		r.log.Error("reconciler: get unresolved markets", "err", err)
		return
	}
	if len(markets) == 0 {
		return
	}

	r.log.Info("reconciler: checking unresolved markets", "count", len(markets))

	finalized := make(map[string]bool)
	resolved := 0
	for _, m := range markets {
		if ctx.Err() != nil {
			return
		}

		mkt, err := r.client.GetMarket(ctx, m.MarketTicker)
		if err != nil {
			r.log.Warn("reconciler: fetch market failed",
				"market", m.MarketTicker, "err", err)
			continue
		}

		// Only update if REST has result or different status
		if mkt.Result == "" && mkt.Status == m.Status {
			continue
		}

		_, err = r.db.UpsertMarketCheckNew(ctx, store.Market{
			MarketTicker:     mkt.Ticker,
			EventTicker:      mkt.EventTicker,
			SeriesTicker:     m.SeriesTicker,
			PlayerName:       mkt.YesSubTitle,
			TennisCompetitor: kalshiclient.ParseTennisCompetitor(mkt.CustomStrike),
			Status:           mkt.Status,
			OccurrenceTS:     kalshiclient.ParseISOTime(mkt.OccurrenceDatetime, r.log),
			OpenTS:           kalshiclient.ParseISOTime(mkt.OpenTime, r.log),
			CloseTS:          kalshiclient.ParseISOTime(mkt.CloseTime, r.log),
			Result:           mkt.Result,
			SettlementTS:     kalshiclient.ParseISOTime(mkt.SettlementTS, r.log),
			SettlementValue:  mkt.SettlementValueDollars,
		})
		if err != nil {
			r.log.Error("reconciler: update market failed",
				"market", m.MarketTicker, "err", err)
			continue
		}

		resolved++
		r.log.Info("reconciler: updated market",
			"market", m.MarketTicker,
			"status", mkt.Status,
			"result", mkt.Result)

		// Run finalization once per event (both markets finalized check inside)
		if mkt.Status == "finalized" && m.EventTicker != "" && !finalized[m.EventTicker] {
			finalized[m.EventTicker] = true
			if err := r.db.FinalizeEventIfNeeded(ctx, m.EventTicker); err != nil {
				r.log.Warn("reconciler: finalize event failed",
					"event", m.EventTicker, "err", err)
			}
			// Resolve all orders for this market
			if mkt.Result != "" {
				if err := r.db.ResolveRealOrders(ctx, m.MarketTicker, mkt.Result); err != nil {
					r.log.Warn("reconciler: resolve real orders failed",
						"market", m.MarketTicker, "err", err)
				} else {
					r.log.Info("reconciler: resolved real orders",
						"market", m.MarketTicker, "result", mkt.Result)
				}
				if err := r.db.ResolveSimulatedOrders(ctx, m.MarketTicker, mkt.Result); err != nil {
					r.log.Warn("reconciler: resolve simulated orders failed",
						"market", m.MarketTicker, "err", err)
				} else {
					r.log.Info("reconciler: resolved simulated orders",
						"market", m.MarketTicker, "result", mkt.Result)
				}
			}
		}
	}

	if resolved > 0 {
		r.log.Info("reconciler: pass complete", "checked", len(markets), "resolved", resolved)
	}
}
