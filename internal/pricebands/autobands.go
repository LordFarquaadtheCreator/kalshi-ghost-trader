package pricebands

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/triggerranges"
)

// AutoBandMinN is the minimum total sample size (aggregated across all days)
// for a band to qualify for auto-addition to trigger_ranges.
const AutoBandMinN = 100

// AutoBandMinWinRate is the minimum aggregate win rate (percent) for a band
// to qualify. Strictly above — a band at exactly 90% does not qualify.
const AutoBandMinWinRate = 90.0

// UpdateAutoBands aggregates price_band_results across all days per
// (strategy, fixed band), finds bands with total N > AutoBandMinN AND
// aggregate win rate > AutoBandMinWinRate, and adds any qualifying bands
// to trigger_ranges that aren't already present (matched by strategy +
// min_price + max_price). Existing ranges are never modified or removed —
// manual dashboard edits are preserved.
//
// Called by the pricebands cron after ComputeMissingDays +
// ComputeMissingInsights so it operates on fresh data.
func UpdateAutoBands(db *store.DB, log *slog.Logger) error {
	rows, err := db.GetAllPriceBandResults()
	if err != nil {
		return err
	}

	type sbKey struct {
		strategy string
		bandIdx  int
	}
	aggs := map[sbKey]*Agg{}
	for _, r := range rows {
		bi := matchFixedBand(r.BandLo, r.BandHi)
		if bi < 0 {
			continue
		}
		k := sbKey{r.Strategy, bi}
		a := aggs[k]
		if a == nil {
			a = &Agg{
				Strategy:  r.Strategy,
				BandIdx:   bi,
				BandLabel: BandLabel(FixedBands[bi]),
				BandLo:    FixedBands[bi].Lo,
				BandHi:    FixedBands[bi].Hi,
			}
			aggs[k] = a
		}
		a.N += r.N
		a.Wins += r.Wins
		a.NetPnL += r.NetPnL
		a.Invested += r.Invested
		a.EdgeSum += r.AvgEdge * float64(r.N)
	}

	source := "pricebands_auto_" + time.Now().UTC().Format("2006-01-02")
	var candidates []store.TriggerRange
	for _, a := range aggs {
		if a.N <= AutoBandMinN {
			continue
		}
		if a.WinRate() <= AutoBandMinWinRate {
			continue
		}
		candidates = append(candidates, store.TriggerRange{
			Strategy: a.Strategy,
			MinPrice: a.BandLo,
			MaxPrice: a.BandHi,
			Source:   source,
			Enabled:  true,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Strategy != candidates[j].Strategy {
			return candidates[i].Strategy < candidates[j].Strategy
		}
		return candidates[i].MinPrice < candidates[j].MinPrice
	})

	if len(candidates) == 0 {
		log.Info("autobands: no bands qualified",
			"min_n", AutoBandMinN, "min_wr", AutoBandMinWinRate)
		return nil
	}

	added, err := triggerranges.AddIfMissing(context.Background(), db.GormDB(), candidates)
	if err != nil {
		return err
	}

	log.Info("autobands: update complete",
		"candidates", len(candidates), "added", added, "skipped", len(candidates)-added,
		"source", source)
	return nil
}

// matchFixedBand returns the FixedBands index whose Lo/Hi match, or -1.
func matchFixedBand(lo, hi float64) int {
	for i, b := range FixedBands {
		if b.Lo == lo && b.Hi == hi {
			return i
		}
	}
	return -1
}
