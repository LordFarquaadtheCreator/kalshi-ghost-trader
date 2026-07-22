package backtest

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// RecomputeIfNeeded checks if new finalized markets appeared since last
// backtest compute. If yes, runs all strategies and persists results to DB.
// Returns true if a recompute happened, false if skipped.
func RecomputeIfNeeded(engine *Engine, db *store.DB, log *slog.Logger) (bool, error) {
	lastRunTS, err := db.GetBacktestRunTS()
	if err != nil {
		return false, err
	}

	lastFinalized, err := db.GetLastFinalizedSettlementTS()
	if err != nil {
		return false, err
	}

	// Skip if no new finalized markets since last compute
	if lastRunTS > 0 && lastFinalized > 0 && lastFinalized <= lastRunTS {
		log.Debug("backtest: no new finalized markets, skipping recompute",
			"last_run_ts", lastRunTS, "last_finalized_ts", lastFinalized)
		return false, nil
	}

	start := time.Now()
	results, err := engine.RunAll(0.0)
	if err != nil {
		log.Error("backtest: RunAll failed", "err", err)
		return false, err
	}

	now := time.Now().UnixMilli()
	for _, res := range results {
		summaryJSON, err := json.Marshal(res.Summary)
		if err != nil {
			log.Error("backtest: marshal summary", "strategy", res.Name, "err", err)
			continue
		}
		ordersJSON, err := json.Marshal(res.Orders)
		if err != nil {
			log.Error("backtest: marshal orders", "strategy", res.Name, "err", err)
			continue
		}
		cumPnLJSON, err := json.Marshal(cumulativePnLSeries(res.Orders))
		if err != nil {
			log.Error("backtest: marshal cum_pnl", "strategy", res.Name, "err", err)
			continue
		}
		row := store.BacktestResultRow{
			Strategy:    res.Name,
			RunTS:       now,
			MatchCount:  res.MatchCount,
			SummaryJSON: string(summaryJSON),
			OrdersJSON:  string(ordersJSON),
			CumPnLJSON:  string(cumPnLJSON),
		}
		if err := db.SaveBacktestResult(row); err != nil {
			log.Error("backtest: save result", "strategy", res.Name, "err", err)
			continue
		}
	}

	log.Info("backtest: recompute complete",
		"strategies", len(results), "elapsed", time.Since(start).Round(time.Millisecond))
	return true, nil
}

// CumPnLPoint is one point in a cumulative P&L series.
type CumPnLPoint struct {
	TS  int64   `json:"ts"`
	PnL float64 `json:"pnl"`
}

// cumulativePnLSeries returns ordered [ts, cum_pnl] pairs from orders,
// sorted by timestamp. Used for the cumulative P&L chart on /simulation.
func cumulativePnLSeries(orders []Order) []CumPnLPoint {
	sorted := make([]Order, len(orders))
	copy(sorted, orders)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].TS < sorted[j].TS
	})
	pts := make([]CumPnLPoint, len(sorted))
	var cum float64
	for i, o := range sorted {
		cum += o.PnL
		pts[i] = CumPnLPoint{TS: o.TS, PnL: mathRound(cum)}
	}
	return pts
}

func mathRound(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
