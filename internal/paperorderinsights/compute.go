// Package paperorderinsights computes per-strategy × per-day × per-band
// derived metrics from the live orders table (paper only, is_real = false).
// Persisted to paper_order_insights + paper_order_summaries by a cron
// goroutine in main.go. Read by /api/paper-orders-insights endpoint.
package paperorderinsights

import (
	"encoding/json"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/pricebands"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// ComputeMissing reads live paper orders, finds days not yet in
// paper_order_insights, computes per-strategy × per-day × per-band derived
// metrics + peak flags, and persists them. Also recomputes
// paper_order_summaries (per-strategy aggregates + cum_pnl series) every run.
func ComputeMissing(db *store.DB, log *slog.Logger) error {
	start := time.Now()

	orders, err := loadPaperOrders(db)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		log.Info("paperorderinsights: no resolved paper orders, nothing to compute")
		return nil
	}

	sourceDays := extractDays(orders)
	computedDays, err := db.GetComputedPaperOrderInsightDays()
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

	runTS := time.Now().UnixMilli()

	if len(missing) > 0 {
		sort.Strings(missing)
		log.Info("paperorderinsights: computing missing days", "count", len(missing), "days", missing)
		for _, day := range missing {
			dayRows := computeInsightDay(orders, day)
			if err := db.SavePaperOrderInsightDay(runTS, day, dayRows); err != nil {
				log.Error("paperorderinsights: save day failed", "day", day, "err", err)
				continue
			}
			log.Info("paperorderinsights: saved day", "day", day, "rows", len(dayRows))
		}
	}

	// Summaries recomputed every run — small table, captures new orders
	// without waiting for day rollover.
	summaries := computeSummaries(orders, runTS)
	if err := db.ReplacePaperOrderSummaries(summaries); err != nil {
		log.Error("paperorderinsights: save summaries failed", "err", err)
	} else {
		log.Info("paperorderinsights: saved summaries", "count", len(summaries))
	}

	log.Info("paperorderinsights: computation complete",
		"days_computed", len(missing), "summaries", len(summaries),
		"elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// paperOrder is a resolved paper order with derived PnL + won flag.
type paperOrder struct {
	TS        int64
	Strategy  string
	Price     float64
	Size      float64
	EdgeCents int
	Won       bool
	PnL       float64
}

func loadPaperOrders(db *store.DB) ([]paperOrder, error) {
	gormDB := db.GormDB()
	var rows []struct {
		TS            int64   `gorm:"column:ts"`
		Strategy      string  `gorm:"column:strategy"`
		MarketPrice   float64 `gorm:"column:market_price"`
		SuggestedSize float64 `gorm:"column:suggested_size"`
		EdgeCents     int     `gorm:"column:edge_cents"`
		Result        string  `gorm:"column:result"`
	}
	// Resolved paper orders only. Market result drives PnL.
	err := gormDB.Raw(`
SELECT o.ts, o.strategy, o.market_price, o.suggested_size, o.edge_cents, m.result
FROM orders o
LEFT JOIN markets m ON o.market_ticker = m.market_ticker
WHERE o.is_real = false
  AND m.result IS NOT NULL AND m.result != ''
  AND o.market_price >= 0.01 AND o.market_price < 0.99
ORDER BY o.ts`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]paperOrder, 0, len(rows))
	for _, r := range rows {
		won := r.Result == "yes"
		var pnl float64
		if won {
			pnl = r.SuggestedSize * (1.0 - r.MarketPrice)
		} else {
			pnl = -r.SuggestedSize * r.MarketPrice
		}
		out = append(out, paperOrder{
			TS: r.TS, Strategy: r.Strategy, Price: r.MarketPrice,
			Size: r.SuggestedSize, EdgeCents: r.EdgeCents,
			Won: won, PnL: pnl,
		})
	}
	return out, nil
}

func extractDays(orders []paperOrder) []string {
	daySet := map[string]bool{}
	for _, o := range orders {
		daySet[pricebands.TSToDay(o.TS)] = true
	}
	days := make([]string, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Strings(days)
	return days
}

// computeInsightDay mirrors pricebands.computeInsightDay but reads from
// live orders table instead of backtest_results.orders_json.
func computeInsightDay(orders []paperOrder, day string) []store.PaperOrderInsightRow {
	type sbKey struct {
		strat   string
		bandIdx int
	}
	dayAggs := map[sbKey]*insightAgg{}

	for _, o := range orders {
		if pricebands.TSToDay(o.TS) != day {
			continue
		}
		bi := pricebands.FindBand(o.Price)
		if bi < 0 {
			continue
		}
		k := sbKey{o.Strategy, bi}
		a := dayAggs[k]
		if a == nil {
			a = &insightAgg{
				strategy:  o.Strategy,
				bandIdx:   bi,
				bandLabel: pricebands.BandLabel(pricebands.FixedBands[bi]),
				bandLo:    pricebands.FixedBands[bi].Lo,
				bandHi:    pricebands.FixedBands[bi].Hi,
			}
			dayAggs[k] = a
		}
		a.orders = append(a.orders, o)
	}

	rows := make([]store.PaperOrderInsightRow, 0, len(dayAggs))
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
	orders       []paperOrder
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
	if lossPnL < 0 {
		a.profitFactor = winPnL / math.Abs(lossPnL)
	} else if winPnL > 0 {
		a.profitFactor = winPnL
	}
	sorted := make([]paperOrder, len(a.orders))
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

func (a *insightAgg) toRow() store.PaperOrderInsightRow {
	return store.PaperOrderInsightRow{
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

// computeSummaries builds per-strategy summary rows + cumulative P&L series.
func computeSummaries(orders []paperOrder, runTS int64) []store.PaperOrderSummaryRow {
	byStrat := map[string][]paperOrder{}
	for _, o := range orders {
		byStrat[o.Strategy] = append(byStrat[o.Strategy], o)
	}
	rows := make([]store.PaperOrderSummaryRow, 0, len(byStrat))
	for strat, stratOrders := range byStrat {
		rows = append(rows, buildSummary(strat, stratOrders, runTS))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Strategy < rows[j].Strategy })
	return rows
}

func buildSummary(strategy string, orders []paperOrder, runTS int64) store.PaperOrderSummaryRow {
	s := store.PaperOrderSummaryRow{Strategy: strategy, RunTS: runTS}
	if len(orders) == 0 {
		return s
	}
	var pnlSum, edgeSum, winPnL, lossPnL, invested float64
	for _, o := range orders {
		s.TotalSignals++
		invested += o.Size * o.Price
		edgeSum += float64(o.EdgeCents)
		if o.Won {
			s.Wins++
			winPnL += o.PnL
		} else {
			s.Losses++
			lossPnL += o.PnL
		}
		pnlSum += o.PnL
	}
	s.TotalInvested = round2(invested)
	s.NetPnL = round2(pnlSum)
	if s.TotalSignals > 0 {
		s.WinRate = float64(s.Wins) / float64(s.TotalSignals) * 100
		s.AvgEdge = edgeSum / float64(s.TotalSignals)
	}
	if invested > 0 {
		s.ROI = pnlSum / invested * 100
	}
	if s.TotalSignals >= 2 {
		mean := pnlSum / float64(s.TotalSignals)
		var sumSq float64
		for _, o := range orders {
			sumSq += o.PnL * o.PnL
		}
		variance := sumSq/float64(s.TotalSignals) - mean*mean
		if variance > 0 {
			s.Sharpe = (mean / math.Sqrt(variance)) * math.Sqrt(float64(s.TotalSignals))
		}
	}
	if lossPnL < 0 {
		s.ProfitFactor = winPnL / math.Abs(lossPnL)
	} else if winPnL > 0 {
		s.ProfitFactor = winPnL
	}
	sorted := make([]paperOrder, len(orders))
	copy(sorted, orders)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TS < sorted[j].TS })
	var cum, peak float64
	cumPts := make([]backtest.CumPnLPoint, 0, len(sorted))
	for _, o := range sorted {
		cum += o.PnL
		if cum > peak {
			peak = cum
		}
		dd := peak - cum
		if dd > s.MaxDrawdown {
			s.MaxDrawdown = dd
		}
		cumPts = append(cumPts, backtest.CumPnLPoint{TS: o.TS, PnL: round2(cum)})
	}
	cumJSON, _ := json.Marshal(cumPts)
	s.CumPnLJSON = string(cumJSON)
	return s
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
