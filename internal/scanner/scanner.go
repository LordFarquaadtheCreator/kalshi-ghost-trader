package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Scanner queries Kalshi for tennis events/markets and stores new ones.
type Scanner struct {
	client    *kalshiclient.Client
	db        *store.DB
	series    []string
	log       *slog.Logger
}

// New creates a scanner for the given series tickers.
func New(client *kalshiclient.Client, db *store.DB, series []string, log *slog.Logger) *Scanner {
	return &Scanner{
		client: client,
		db:     db,
		series: series,
		log:    log,
	}
}

// RunOnce scans all configured series and stores new events/markets.
// Returns counts of new events and new markets found. Errors from individual
// series are joined — one failing series doesn't mask others.
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
	// Single call with no status filter — gets all events (open, closed, settled).
	// The previous double-scan (status=open + all-status) was redundant since
	// the all-status call is a superset.
	events, err := s.client.GetEvents(ctx, seriesTicker, "")
	if err != nil {
		return 0, 0, err
	}

	totalMarkets := 0
	for _, ev := range events {
		// Store event — check if new by querying first
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

		// Get markets for this event (always exactly 2 for tennis)
		markets, err := s.client.GetMarkets(ctx, "", ev.EventTicker, "")
		if err != nil {
			s.log.Error("get markets for event", "event", ev.EventTicker, "err", err)
			continue
		}

		for _, mkt := range markets {
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

	// Record scan run
	_ = s.db.RecordScanRun(ctx, seriesTicker, len(events), totalMarkets, newEvents, newMarkets)
	s.log.Info("series scanned", "series", seriesTicker, "events", len(events), "markets", totalMarkets, "new_events", newEvents, "new_markets", newMarkets)
	return
}

// RunLoop runs the scan on a fixed interval until ctx cancelled.
func (s *Scanner) RunLoop(ctx context.Context, interval time.Duration) error {
	// Run immediately on start
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
		}
	}
}
