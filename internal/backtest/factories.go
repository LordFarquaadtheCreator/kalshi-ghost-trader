package backtest

import (
	"log/slog"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
)

// DefaultFactories returns the standard set of strategy factories used by
// both the backtest CLI and the strategy API server.
func DefaultFactories() map[string]StrategyFactory {
	return map[string]StrategyFactory{
		"matchpoint": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewMatchPointStrategy(em, log)
		},
		"matchpoint-aggro": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewSetPointStrategy(em, log, algorithms.SetPointConfig{
				IncludeSetPoints: false,
				IncludeReturning: true,
				ServeConvProb:    0.97,
				ReturnConvProb:   0.89,
				MinMarketPrice:   0.05,
				MinEdgeCents:     1,
				Label:            "matchpoint-aggro",
			})
		},
		"setpoint": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewSetPointStrategy(em, log, algorithms.DefaultSetPointConfig())
		},
		"setpoint-serve": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.IncludeReturning = false
			cfg.Label = "setpoint-serve"
			return algorithms.NewSetPointStrategy(em, log, cfg)
		},
		"setpoint-cheap": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.MaxMarketPrice = 0.50
			cfg.Label = "setpoint-cheap"
			return algorithms.NewSetPointStrategy(em, log, cfg)
		},
		"fadelongshot": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewFadeLongshotStrategy(em, log, algorithms.DefaultFadeLongshotConfig())
		},
	}
}
