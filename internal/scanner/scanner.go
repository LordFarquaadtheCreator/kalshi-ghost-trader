// Package scanner implements a periodic REST scan of Kalshi tennis series.
//
// The Scanner queries the Kalshi REST API for all events and markets in each
// configured series ticker, upserts new entries into PostgreSQL, and records an
// audit log via scan_runs. It detects dead scans (series returning zero events
// after previously returning >500) as a sentinel for API or auth failures.
//
// Events with all markets finalized that don't already exist in the DB are
// skipped entirely — no insert-then-delete churn. Scalar markets are filtered
// out; only match-winner (binary) markets are stored.
//
// After each scan cycle, the scanner runs janitor cleanup: CleanOrphans removes
// orphaned child rows older than 6 hours, and AdoptOrphans attempts to parent
// orphan event lifecycle events by creating missing event records.
//
// Errors per-series are logged but don't abort the scan — other series continue.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Scanner queries Kalshi for tennis events/markets and stores new ones.
type Scanner struct {
	client *kalshiclient.Client
	db     *store.DB
	series []string
	log    *slog.Logger

	// P9: per-series last event count for dead-scan detection
	lastCounts map[string]int
}

// New creates a scanner. series tickers are read from config.Cfg.SeriesTickers.
func New(client *kalshiclient.Client, db *store.DB, log *slog.Logger) *Scanner {
	return &Scanner{
		client:     client,
		db:         db,
		series:     config.Cfg.SeriesTickers,
		log:        log,
		lastCounts: make(map[string]int),
	}
}

// RunOnce scans all configured series and stores new events/markets.
func (s *Scanner) RunOnce(ctx context.Context) (newEvents, newMarkets int, err error) {
	var errs []error
	for _, series := range s.series {
		ne, nm, e := s.scanSeries(ctx, series)
		if e != nil {
			s.log.Error("scan series failed", "series", series, "err", e)
			errs = append(errs, fmt.Errorf("series %s: %w", series, e))
			continue
		}
		newEvents += ne
		newMarkets += nm
	}
	s.log.Info("scan complete", "new_events", newEvents, "new_markets", newMarkets)
	if len(errs) > 0 {
		err = errors.Join(errs...)
	}
	return
}

// scanSeries queries one series for all events + their markets.
func (s *Scanner) scanSeries(ctx context.Context, seriesTicker string) (newEvents, newMarkets int, err error) {
	events, err := s.client.GetEvents(ctx, seriesTicker, "")
	if err != nil {
		return 0, 0, err
	}

	// P9: dead-scan sentinel — if we previously saw events for this series
	// and now get zero, something is wrong (API change, auth failure).
	prevCount := s.lastCounts[seriesTicker]
	s.lastCounts[seriesTicker] = len(events)
	if prevCount > 500 && len(events) == 0 {
		s.log.Error("dead scan detected — series returned zero events after historical non-zero",
			"series", seriesTicker, "prev_count", prevCount)
	}

	totalMarkets := 0
	for _, ev := range events {
		// P1: Fetch markets FIRST so we can decide whether to bother upserting.
		// If the event doesn't exist yet and markets are all finalized, skip it
		// entirely — no insert, no delete, no trigger churn.
		markets, err := s.client.GetMarkets(ctx, "", ev.EventTicker, "")
		if err != nil {
			s.log.Error("get markets for event", "event", ev.EventTicker, "err", err)
			continue
		}

		// P1: Check if all markets are finalized AND event isn't in DB yet.
		// Skip insert-then-delete — just skip the insert entirely.
		allFinalized := len(markets) > 0
		for _, mkt := range markets {
			if mkt.Status != "finalized" {
				allFinalized = false
				break
			}
		}

		if allFinalized {
			exists, err := s.db.EventExists(ctx, ev.EventTicker)
			if err == nil && !exists {
				continue // historical stub — never inserted, never tracked
			}
			// Exists — we've tracked this event before, update it below
		}

		// Upsert event
		isNew, err := s.db.UpsertEventCheckNew(ctx, store.Event{
			EventTicker:       ev.EventTicker,
			SeriesTicker:      ev.SeriesTicker,
			Title:             ev.Title,
			SubTitle:          ev.SubTitle,
			Competition:       ev.ProductMetadata.Competition,
			CompetitionScope:  ev.ProductMetadata.CompetitionScope,
			MutuallyExclusive: ev.MutuallyExclusive,
		})
		if err != nil {
			s.log.Error("upsert event", "ticker", ev.EventTicker, "err", err)
			continue
		}

		if isNew {
			newEvents++
		}

		for _, mkt := range markets {
			if mkt.MarketType == "scalar" {
				continue
			}
			tennisCompetitor := kalshiclient.ParseTennisCompetitor(mkt.CustomStrike)
			isNewM, err := s.db.UpsertMarketCheckNew(ctx, store.Market{
				MarketTicker:     mkt.Ticker,
				EventTicker:      mkt.EventTicker,
				SeriesTicker:     seriesTicker,
				PlayerName:       mkt.YesSubTitle,
				TennisCompetitor: tennisCompetitor,
				Status:           mkt.Status,
				OccurrenceTS:     kalshiclient.ParseISOTime(mkt.OccurrenceDatetime, s.log),
				OpenTS:           kalshiclient.ParseISOTime(mkt.OpenTime, s.log),
				CloseTS:          kalshiclient.ParseISOTime(mkt.CloseTime, s.log),
				Result:           mkt.Result,
				SettlementTS:     kalshiclient.ParseISOTime(mkt.SettlementTS, s.log),
				SettlementValue:  mkt.SettlementValueDollars,
			})
			if err != nil {
				s.log.Error("upsert market", "ticker", mkt.Ticker, "err", err)
				continue
			}
			if isNewM {
				newMarkets++
			}
			totalMarkets++
		}
	}

	_ = s.db.RecordScanRun(ctx, seriesTicker, len(events), totalMarkets, newEvents, newMarkets)
	s.log.Info("series scanned", "series", seriesTicker, "events", len(events), "markets", totalMarkets, "new_events", newEvents, "new_markets", newMarkets)
	return
}

// RunLoop runs the scan on a fixed interval until ctx cancelled.
func (s *Scanner) RunLoop(ctx context.Context, interval time.Duration) error {
	if _, _, err := s.RunOnce(ctx); err != nil {
		s.log.Error("initial scan failed", "err", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, _, err := s.RunOnce(ctx); err != nil {
				s.log.Error("scan failed", "err", err)
			}
			// P3: Run janitor cleanups after each scan cycle
			if err := s.db.CleanOrphans(ctx, s.log); err != nil {
				s.log.Error("clean orphans failed", "err", err)
			}
			// P4: Attempt to adopt young orphans
			if err := s.db.AdoptOrphans(ctx, s.log); err != nil {
				s.log.Error("adopt orphans failed", "err", err)
			}
		}
	}
}
