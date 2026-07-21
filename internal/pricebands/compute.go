package pricebands

import (
	"log/slog"
	"sort"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// ComputeMissingDays runs all strategies, finds days not yet in the DB,
// computes fixed-band aggregates for those days, and persists them.
//
// RunAll is called once; missing days are filtered from the same result set.
// Most cron runs find 0-1 new days and do minimal work.
func ComputeMissingDays(engine *backtest.Engine, db *store.DB, log *slog.Logger) error {
	start := time.Now()

	log.Info("pricebands: running all strategies")
	results, err := engine.RunAll(0)
	if err != nil {
		return err
	}

	// Extract all distinct days from order timestamps
	sourceDays := extractDays(results)
	if len(sourceDays) == 0 {
		log.Info("pricebands: no orders found, nothing to compute")
		return nil
	}

	// Get already computed days
	computedDays, err := db.GetComputedDays()
	if err != nil {
		return err
	}
	computedSet := make(map[string]bool, len(computedDays))
	for _, d := range computedDays {
		computedSet[d] = true
	}

	// Diff: source days not yet computed
	var missing []string
	for _, d := range sourceDays {
		if !computedSet[d] {
			missing = append(missing, d)
		}
	}

	if len(missing) == 0 {
		log.Info("pricebands: all days already computed", "source_days", len(sourceDays), "elapsed", time.Since(start).Round(time.Millisecond))
		return nil
	}

	sort.Strings(missing)
	log.Info("pricebands: computing missing days", "count", len(missing), "days", missing, "elapsed", time.Since(start).Round(time.Millisecond))

	runTS := time.Now().UnixMilli()

	for _, day := range missing {
		rows := computeDay(results, day)
		if err := db.SavePriceBandDay(runTS, day, rows); err != nil {
			log.Error("pricebands: save day failed", "day", day, "err", err)
			continue
		}
		log.Info("pricebands: saved day", "day", day, "rows", len(rows))
	}

	log.Info("pricebands: computation complete", "days_computed", len(missing), "elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// extractDays returns sorted distinct day strings from all strategy results.
func extractDays(results []*backtest.StrategyResult) []string {
	daySet := map[string]bool{}
	for _, res := range results {
		for _, o := range res.Orders {
			if o.Price < 0.01 || o.Price >= 0.99 {
				continue
			}
			daySet[TSToDay(o.TS)] = true
		}
	}
	days := make([]string, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Strings(days)
	return days
}

// computeDay aggregates orders for a specific day into per-strategy-per-band rows.
func computeDay(results []*backtest.StrategyResult, day string) []store.PriceBandResultRow {
	type sbKey struct {
		strat   string
		bandIdx int
	}
	dayAggs := map[sbKey]*Agg{}

	for _, res := range results {
		for _, o := range res.Orders {
			if o.Price < 0.01 || o.Price >= 0.99 {
				continue
			}
			bi := FindBand(o.Price)
			if bi < 0 {
				continue
			}
			if TSToDay(o.TS) != day {
				continue
			}
			k := sbKey{res.Name, bi}
			a := dayAggs[k]
			if a == nil {
				a = &Agg{
					Strategy:  res.Name,
					BandIdx:   bi,
					BandLabel: BandLabel(FixedBands[bi]),
					BandLo:    FixedBands[bi].Lo,
					BandHi:    FixedBands[bi].Hi,
				}
				dayAggs[k] = a
			}
			a.N++
			if o.Won {
				a.Wins++
			}
			a.NetPnL += o.PnL
			a.Invested += o.Size * o.Price
			a.EdgeSum += float64(o.EdgeCents)
		}
	}

	rows := make([]store.PriceBandResultRow, 0, len(dayAggs))
	for _, a := range dayAggs {
		if a.N == 0 {
			continue
		}
		rows = append(rows, store.PriceBandResultRow{
			Strategy:  a.Strategy,
			BandLabel: a.BandLabel,
			BandLo:    a.BandLo,
			BandHi:    a.BandHi,
			N:         a.N,
			Wins:      a.Wins,
			WinRate:   a.WinRate(),
			NetPnL:    a.NetPnL,
			Invested:  a.Invested,
			ROI:       a.ROI(),
			AvgEdge:   a.AvgEdge(),
		})
	}
	return rows
}
