package pricebands

import (
	"encoding/json"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// ComputeMissingInsights reads persisted backtest results, finds days not
// yet in simulation_insights, computes per-strategy × per-day × per-band
// derived metrics (sharpe, profit_factor, max_drawdown, score, peak),
// and persists them. Runs alongside ComputeMissingDays in the same cron.
func ComputeMissingInsights(db *store.DB, log *slog.Logger) error {
	start := time.Now()

	rows, err := db.GetAllBacktestResults()
	if err != nil {
		return err
	}

	var allOrders []stratOrders
	for _, row := range rows {
		var orders []backtest.Order
		if err := json.Unmarshal([]byte(row.OrdersJSON), &orders); err != nil {
			log.Error("insights: unmarshal orders", "strategy", row.Strategy, "err", err)
			continue
		}
		allOrders = append(allOrders, stratOrders{name: row.Strategy, orders: orders})
	}

	sourceDays := extractDaysFromOrders(allOrders)
	if len(sourceDays) == 0 {
		log.Info("insights: no orders found, nothing to compute")
		return nil
	}

	computedDays, err := db.GetComputedInsightDays()
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
		log.Info("insights: all days already computed",
			"source_days", len(sourceDays), "elapsed", time.Since(start).Round(time.Millisecond))
		return nil
	}

	sort.Strings(missing)
	log.Info("insights: computing missing days", "count", len(missing), "days", missing)

	runTS := time.Now().UnixMilli()

	for _, day := range missing {
		dayRows := computeInsightDay(allOrders, day)
		if err := db.SaveSimulationInsightDay(runTS, day, dayRows); err != nil {
			log.Error("insights: save day failed", "day", day, "err", err)
			continue
		}
		log.Info("insights: saved day", "day", day, "rows", len(dayRows))
	}

	log.Info("insights: computation complete",
		"days_computed", len(missing), "elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// computeInsightDay builds per-strategy × per-band rows for a single day,
// including derived metrics and peak detection across bands within each strategy.
func computeInsightDay(allOrders []stratOrders, day string) []store.SimulationInsightRow {
	type sbKey struct {
		strat   string
		bandIdx int
	}
	dayAggs := map[sbKey]*insightAgg{}

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
				a = &insightAgg{
					strategy:  so.name,
					bandIdx:   bi,
					bandLabel: BandLabel(FixedBands[bi]),
					bandLo:    FixedBands[bi].Lo,
					bandHi:    FixedBands[bi].Hi,
				}
				dayAggs[k] = a
			}
			a.orders = append(a.orders, o)
		}
	}

	// Compute derived metrics per band, then detect peaks per strategy.
	rows := make([]store.SimulationInsightRow, 0, len(dayAggs))
	stratBands := map[string][]*insightAgg{}

	for _, a := range dayAggs {
		a.compute()
		stratBands[a.strategy] = append(stratBands[a.strategy], a)
	}

	for _, bands := range stratBands {
		detectFixedPeaks(bands)
		for _, a := range bands {
			rows = append(rows, a.toRow())
		}
	}

	return rows
}

type insightAgg struct {
	strategy     string
	bandIdx      int
	bandLabel    string
	bandLo       float64
	bandHi       float64
	orders       []backtest.Order
	n            int
	wins         int
	winRate      float64
	netPnL       float64
	invested     float64
	roi          float64
	avgEdge      float64
	sharpe       float64
	profitFactor float64
	maxDrawdown  float64
	score        float64
	peak         bool
}

func (a *insightAgg) compute() {
	a.n = len(a.orders)
	if a.n == 0 {
		return
	}

	var pnlSum, edgeSum, winPnL, lossPnL float64
	for _, o := range a.orders {
		if o.Won {
			a.wins++
			winPnL += o.PnL
		} else {
			lossPnL += o.PnL
		}
		pnlSum += o.PnL
		a.invested += o.Size * o.Price
		edgeSum += float64(o.EdgeCents)
	}

	a.winRate = float64(a.wins) / float64(a.n) * 100
	a.netPnL = round2(pnlSum)
	if a.invested > 0 {
		a.roi = pnlSum / a.invested * 100
	}
	a.avgEdge = edgeSum / float64(a.n)

	// Sharpe: mean/std * sqrt(n)
	if a.n >= 2 {
		mean := pnlSum / float64(a.n)
		var sumSq float64
		for _, o := range a.orders {
			sumSq += o.PnL * o.PnL
		}
		variance := sumSq/float64(a.n) - mean*mean
		if variance > 0 {
			a.sharpe = (mean / math.Sqrt(variance)) * math.Sqrt(float64(a.n))
		}
	}

	// Profit factor: gross profit / gross loss
	if lossPnL < 0 {
		a.profitFactor = winPnL / math.Abs(lossPnL)
	} else if winPnL > 0 {
		a.profitFactor = winPnL
	}

	// Max drawdown: peak-to-trough on cumulative P&L
	sorted := make([]backtest.Order, len(a.orders))
	copy(sorted, a.orders)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TS < sorted[j].TS })
	var cum, peak float64
	for _, o := range sorted {
		cum += o.PnL
		if cum > peak {
			peak = cum
		}
		dd := peak - cum
		if dd > a.maxDrawdown {
			a.maxDrawdown = dd
		}
	}

	// Score: winrate fraction * ln(n+1) — same metric as WinRateScore
	a.score = (float64(a.wins) / float64(a.n)) * math.Log(float64(a.n+1))
}

// detectFixedPeaks marks bands that are local maxima above the median score
// within a single strategy-day. Only bands with n >= 2 are candidates.
func detectFixedPeaks(bands []*insightAgg) {
	if len(bands) < 3 {
		return
	}

	scores := make([]float64, 0, len(bands))
	for _, b := range bands {
		if b.n >= 2 {
			scores = append(scores, b.score)
		}
	}
	if len(scores) < 3 {
		return
	}
	sort.Float64s(scores)
	median := scores[len(scores)/2]

	// Sort bands by bandIdx for neighbor comparison
	sort.Slice(bands, func(i, j int) bool { return bands[i].bandIdx < bands[j].bandIdx })

	for i, b := range bands {
		if b.n < 2 || b.score <= median {
			continue
		}
		left := i > 0
		right := i < len(bands)-1
		leftOK := !left || b.score > bands[i-1].score
		rightOK := !right || b.score > bands[i+1].score
		if leftOK && rightOK {
			b.peak = true
		}
	}
}

func (a *insightAgg) toRow() store.SimulationInsightRow {
	return store.SimulationInsightRow{
		Strategy:     a.strategy,
		BandLabel:    a.bandLabel,
		BandLo:       a.bandLo,
		BandHi:       a.bandHi,
		N:            a.n,
		Wins:         a.wins,
		WinRate:      a.winRate,
		NetPnL:       a.netPnL,
		Invested:     a.invested,
		ROI:          a.roi,
		AvgEdge:      a.avgEdge,
		Sharpe:       a.sharpe,
		ProfitFactor: a.profitFactor,
		MaxDrawdown:  a.maxDrawdown,
		Score:        a.score,
		Peak:         a.peak,
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
