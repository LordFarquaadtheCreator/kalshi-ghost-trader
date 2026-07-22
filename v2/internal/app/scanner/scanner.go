// Package scanner implements a periodic REST scan of Kalshi tennis series.
// Queries for events/markets, upserts into the events/markets tables.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/kalshi"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/kalshi/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Scanner queries Kalshi for tennis events/markets and stores new ones.
type Scanner struct {
	client *kalshi.RESTClient
	db     *gorm.DB
	series []string
	log    *slog.Logger
}

// New creates a scanner.
func New(client *kalshi.RESTClient, db *gorm.DB, series []string, log *slog.Logger) *Scanner {
	return &Scanner{
		client: client,
		db:     db,
		series: series,
		log:    log,
	}
}

// RunOnce scans all configured series.
func (s *Scanner) RunOnce(ctx context.Context) (newEvents, newMarkets int, err error) {
	var errs []error
	for _, series := range s.series {
		ne, nm, e := s.scanSeries(ctx, series)
		if e != nil {
			s.log.Error("scanner: series failed", "series", series, "err", e)
			errs = append(errs, fmt.Errorf("series %s: %w", series, e))
			continue
		}
		newEvents += ne
		newMarkets += nm
	}
	s.log.Info("scanner: scan complete", "new_events", newEvents, "new_markets", newMarkets)
	if len(errs) > 0 {
		err = errors.Join(errs...)
	}
	return
}

func (s *Scanner) scanSeries(ctx context.Context, seriesTicker string) (int, int, error) {
	events, err := s.client.GetEvents(ctx, seriesTicker)
	if err != nil {
		return 0, 0, fmt.Errorf("get events: %w", err)
	}

	newEvents, newMarkets := 0, 0
	for _, ev := range events {
		markets, err := s.client.GetMarkets(ctx, ev.EventTicker)
		if err != nil {
			s.log.Error("scanner: get markets", "event", ev.EventTicker, "err", err)
			continue
		}

		// Skip events where all markets are finalized and event doesn't exist yet.
		allFinalized := len(markets) > 0
		for _, mkt := range markets {
			if mkt.Status != "finalized" {
				allFinalized = false
				break
			}
		}
		if allFinalized {
			var count int64
			s.db.WithContext(ctx).Table("events").Where("event_ticker = ?", ev.EventTicker).Count(&count)
			if count == 0 {
				continue
			}
		}

		// Upsert event.
		isNew, err := s.upsertEvent(ctx, ev, seriesTicker)
		if err != nil {
			s.log.Error("scanner: upsert event", "ticker", ev.EventTicker, "err", err)
			continue
		}
		if isNew {
			newEvents++
		}

		for _, mkt := range markets {
			if mkt.MarketType == "scalar" {
				continue
			}
			isNewM, err := s.upsertMarket(ctx, mkt, seriesTicker)
			if err != nil {
				s.log.Error("scanner: upsert market", "ticker", mkt.Ticker, "err", err)
				continue
			}
			if isNewM {
				newMarkets++
			}
		}
	}

	s.log.Info("scanner: series done", "series", seriesTicker, "events", len(events), "new_events", newEvents, "new_markets", newMarkets)
	return newEvents, newMarkets, nil
}

type eventRow struct {
	EventTicker       string `gorm:"column:event_ticker;primaryKey"`
	SeriesTicker      string `gorm:"column:series_ticker"`
	Title             string `gorm:"column:title"`
	SubTitle          string `gorm:"column:sub_title"`
	Competition       string `gorm:"column:competition"`
	CompetitionScope  string `gorm:"column:competition_scope"`
	MutuallyExclusive bool   `gorm:"column:mutually_exclusive"`
}

func (eventRow) TableName() string { return "events" }

func (s *Scanner) upsertEvent(ctx context.Context, ev wire.EventData, seriesTicker string) (bool, error) {
	row := eventRow{
		EventTicker:       ev.EventTicker,
		SeriesTicker:      ev.SeriesTicker,
		Title:             ev.Title,
		SubTitle:          ev.SubTitle,
		Competition:       ev.ProductMetadata.Competition,
		CompetitionScope:  ev.ProductMetadata.CompetitionScope,
		MutuallyExclusive: ev.MutuallyExclusive,
	}

	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_ticker"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		return true, nil
	}

	// Row existed — update.
	return false, s.db.WithContext(ctx).Model(&eventRow{}).
		Where("event_ticker = ?", ev.EventTicker).
		Updates(map[string]any{
			"title":              ev.Title,
			"sub_title":          ev.SubTitle,
			"competition":        ev.ProductMetadata.Competition,
			"competition_scope":  ev.ProductMetadata.CompetitionScope,
			"mutually_exclusive": ev.MutuallyExclusive,
		}).Error
}

type marketRow struct {
	MarketTicker    string `gorm:"column:market_ticker;primaryKey"`
	EventTicker     string `gorm:"column:event_ticker"`
	SeriesTicker    string `gorm:"column:series_ticker"`
	PlayerName      string `gorm:"column:player_name"`
	Status          string `gorm:"column:status"`
	OccurrenceTS    int64  `gorm:"column:occurrence_ts"`
	OpenTS          int64  `gorm:"column:open_ts"`
	CloseTS         int64  `gorm:"column:close_ts"`
	Result          string `gorm:"column:result"`
	SettlementTS    int64  `gorm:"column:settlement_ts"`
	SettlementValue string `gorm:"column:settlement_value"`
}

func (marketRow) TableName() string { return "markets" }

func (s *Scanner) upsertMarket(ctx context.Context, mkt wire.MarketData, seriesTicker string) (bool, error) {
	row := marketRow{
		MarketTicker:    mkt.Ticker,
		EventTicker:     mkt.EventTicker,
		SeriesTicker:    seriesTicker,
		PlayerName:      mkt.YesSubTitle,
		Status:          mkt.Status,
		OccurrenceTS:    kalshi.ParseISOTime(mkt.OccurrenceDatetime),
		OpenTS:          kalshi.ParseISOTime(mkt.OpenTime),
		CloseTS:         kalshi.ParseISOTime(mkt.CloseTime),
		Result:          mkt.Result,
		SettlementTS:    kalshi.ParseISOTime(mkt.SettlementTS),
		SettlementValue: mkt.SettlementValueDollars,
	}

	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "market_ticker"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		return true, nil
	}

	return false, s.db.WithContext(ctx).Model(&marketRow{}).
		Where("market_ticker = ?", mkt.Ticker).
		Updates(map[string]any{
			"status":           mkt.Status,
			"result":           mkt.Result,
			"settlement_ts":    kalshi.ParseISOTime(mkt.SettlementTS),
			"settlement_value": mkt.SettlementValueDollars,
		}).Error
}

// RunLoop runs the scan on a fixed interval until ctx cancelled.
func (s *Scanner) RunLoop(ctx context.Context, interval time.Duration) error {
	if _, _, err := s.RunOnce(ctx); err != nil {
		s.log.Error("scanner: initial scan failed", "err", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, _, err := s.RunOnce(ctx); err != nil {
				s.log.Error("scanner: scan failed", "err", err)
			}
		}
	}
}
