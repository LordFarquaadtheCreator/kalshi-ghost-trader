package backtest

import (
	"log/slog"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
)

// DefaultFactories returns the standard set of strategy factories used by
// both the backtest CLI and the strategy API server.
func DefaultFactories() map[string]StrategyFactory {
	return map[string]StrategyFactory{
		"matchpoint": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			return algorithms.NewMatchPointStrategy(em, log, bankroll, kellyFraction)
		},
		"matchpoint-aggro": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			return algorithms.NewSetPointStrategy(em, log, algorithms.SetPointConfig{
				IncludeSetPoints: false,
				IncludeReturning: true,
				ServeConvProb:    0.97,
				ReturnConvProb:   0.89,
				MinMarketPrice:   0.05,
				MinEdgeCents:     1,
				Label:            "matchpoint-aggro",
			}, bankroll, kellyFraction)
		},
		"setpoint": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			return algorithms.NewSetPointStrategy(em, log, algorithms.DefaultSetPointConfig(), bankroll, kellyFraction)
		},
		"setpoint-serve": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.IncludeReturning = false
			cfg.Label = "setpoint-serve"
			return algorithms.NewSetPointStrategy(em, log, cfg, bankroll, kellyFraction)
		},
		"setpoint-cheap": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.MaxMarketPrice = 0.50
			cfg.Label = "setpoint-cheap"
			return algorithms.NewSetPointStrategy(em, log, cfg, bankroll, kellyFraction)
		},
		"fadelongshot": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			return algorithms.NewFadeLongshotStrategy(em, log, algorithms.DefaultFadeLongshotConfig(), bankroll, kellyFraction)
		},
		"nofade": func(em algorithms.OrderEmitter, log *slog.Logger, bankroll, kellyFraction float64) ReplayStrategy {
			return algorithms.NewNoFadeStrategy(em, log, algorithms.DefaultNoFadeConfig(), bankroll, kellyFraction)
		},
	}
}
