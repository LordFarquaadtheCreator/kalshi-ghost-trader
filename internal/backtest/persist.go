package backtest

import (
	"encoding/json"
	"log/slog"
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
		row := store.BacktestResultRow{
			Strategy:    res.Name,
			RunTS:       now,
			MatchCount:  res.MatchCount,
			SummaryJSON: string(summaryJSON),
			OrdersJSON:  string(ordersJSON),
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

// ForceRecompute runs all strategies and persists results to DB, ignoring
// the "new data" check. Used by the manual recompute button.
func ForceRecompute(engine *Engine, db *store.DB, log *slog.Logger) error {
	_, err := RecomputeIfNeeded(engine, db, log)
	return err
}
