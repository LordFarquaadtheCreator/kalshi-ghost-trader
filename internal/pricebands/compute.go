package pricebands

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// stratOrders pairs a strategy name with its unmarshalled orders.
type stratOrders struct {
	name   string
	orders []backtest.Order
}

// ComputeMissingDays reads persisted backtest results from DB, finds days
// not yet in price_band_results, computes fixed-band aggregates for those
// days, and persists them.
//
// No engine.RunAll call — reads orders_json from backtest_results table.
func ComputeMissingDays(db *store.DB, log *slog.Logger) error {
	start := time.Now()

	rows, err := db.GetAllBacktestResults()
	if err != nil {
		return err
	}

	var allOrders []stratOrders
	for _, row := range rows {
		var orders []backtest.Order
		if err := json.Unmarshal([]byte(row.OrdersJSON), &orders); err != nil {
			log.Error("pricebands: unmarshal orders", "strategy", row.Strategy, "err", err)
			continue
		}
		allOrders = append(allOrders, stratOrders{name: row.Strategy, orders: orders})
	}

	sourceDays := extractDaysFromOrders(allOrders)
	if len(sourceDays) == 0 {
		log.Info("pricebands: no orders found, nothing to compute")
		return nil
	}

	computedDays, err := db.GetComputedDays()
	if err != nil {
		return err
	}
	computedSet := make(map[string]bool, len(computedDays))
	for _, d := range computedDays {
		computedSet[d] = true
	}

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
		dayRows := computeDayFromOrders(allOrders, day)
		if err := db.SavePriceBandDay(runTS, day, dayRows); err != nil {
			log.Error("pricebands: save day failed", "day", day, "err", err)
			continue
		}
		log.Info("pricebands: saved day", "day", day, "rows", len(dayRows))
	}

	log.Info("pricebands: computation complete", "days_computed", len(missing), "elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

func extractDaysFromOrders(allOrders []stratOrders) []string {
	daySet := map[string]bool{}
	for _, so := range allOrders {
		for _, o := range so.orders {
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

func computeDayFromOrders(allOrders []stratOrders, day string) []store.PriceBandResultRow {
	type sbKey struct {
		strat   string
		bandIdx int
	}
	dayAggs := map[sbKey]*Agg{}

	for _, so := range allOrders {
		for _, o := range so.orders {
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
			k := sbKey{so.name, bi}
			a := dayAggs[k]
			if a == nil {
				a = &Agg{
					Strategy:  so.name,
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
