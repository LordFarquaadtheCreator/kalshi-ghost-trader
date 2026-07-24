package backtest

import (
	"log/slog"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
)

// DefaultFactories returns the standard set of strategy factories used by
// both the backtest CLI and the strategy API server.
func DefaultFactories() map[string]StrategyFactory {
	// R.8: shared Markov model for all pServe=0.64 strategies. Memoization
	// works across strategies — same score state computed once. Model is
	// mutex-guarded; safe for concurrent use across parallel replay goroutines.
	// calibrated-markov + surface-markov keep per-call models (different pServe).
	sharedMarkov := algorithms.NewMarkovModel()

	return map[string]StrategyFactory{
		"matchpoint": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewMatchPointStrategy(em, log)
		},
		"matchpoint-aggro": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			s := algorithms.NewSetPointStrategy(em, log, algorithms.SetPointConfig{
				IncludeSetPoints: false,
				IncludeReturning: true,
				IncludeServing:   true,
				PServe:           0.64,
				MinMarketPrice:   0.05,
				MinEdgeCents:     5,
				CooldownPoints:   3,
				Label:            "matchpoint-aggro",
			})
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"setpoint": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			s := algorithms.NewSetPointStrategy(em, log, algorithms.DefaultSetPointConfig())
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"setpoint-serve": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.IncludeReturning = false
			cfg.Label = "setpoint-serve"
			s := algorithms.NewSetPointStrategy(em, log, cfg)
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"setpoint-cheap": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.MaxMarketPrice = 0.50
			cfg.Label = "setpoint-cheap"
			s := algorithms.NewSetPointStrategy(em, log, cfg)
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"fadelongshot": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewFadeLongshotStrategy(em, log, algorithms.DefaultFadeLongshotConfig())
		},
		"nofade": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewNoFadeStrategy(em, log, algorithms.DefaultNoFadeConfig())
		},
		"breakback": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewBreakBackStrategy(em, log, algorithms.DefaultBreakBackConfig())
		},
		"setdown": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewSetDownStrategy(em, log, algorithms.DefaultSetDownConfig())
		},
		"server1530": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewServer1530Strategy(em, log, algorithms.DefaultServer1530Config())
		},
		"tiebreak": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewTiebreakStrategy(em, log, algorithms.DefaultTiebreakConfig())
		},
		"breakpoint": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			s := algorithms.NewBreakPointStrategy(em, log, algorithms.DefaultBreakPointConfig())
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"convexpool": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			s := algorithms.NewConvexPoolStrategy(em, log, algorithms.DefaultConvexPoolConfig())
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"comeback040": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewComeback040Strategy(em, log, algorithms.DefaultComeback040Config())
		},
		"calibrated-markov": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewCalibratedMarkovStrategy(em, log, algorithms.DefaultCalibratedMarkovConfig())
		},
		"cross-arb": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewCrossArbStrategy(em, log, algorithms.DefaultCrossArbConfig())
		},
		"cross-arb-favorite": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewCrossArbFavoriteStrategy(em, log, algorithms.DefaultCrossArbFavoriteConfig())
		},
		"tiebreak-server": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewTiebreakServerStrategy(em, log, algorithms.DefaultTiebreakServerConfig())
		},
		"set1winner": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewSet1WinnerStrategy(em, log, algorithms.DefaultSet1WinnerConfig())
		},
		"volratio": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewVolumeRatioStrategy(em, log, algorithms.DefaultVolumeRatioConfig())
		},
		"surface-markov": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewSurfaceMarkovStrategy(em, log, algorithms.DefaultSurfaceMarkovConfig())
		},
		"spike-fade": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewSpikeFadeStrategy(em, log, algorithms.DefaultSpikeFadeConfig())
		},
		"buythedip": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewBuyTheDipStrategy(em, log, algorithms.DefaultBuyTheDipConfig())
		},
		"doublebreak": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewDoubleBreakStrategy(em, log, algorithms.DefaultDoubleBreakConfig())
		},
		"bookpressure": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			return algorithms.NewBookPressureStrategy(em, log, algorithms.DefaultBookPressureConfig())
		},
		"bookpressure-strict": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultBookPressureConfig()
			cfg.MinPressure = 0.70
			cfg.CooldownSeconds = 180
			cfg.MinBidSize = 500
			cfg.MinAskSize = 500
			cfg.TakeProfitCents = 3
			cfg.StopLossCents = 2
			cfg.Label = "bookpressure-strict"
			return algorithms.NewBookPressureStrategy(em, log, cfg)
		},
		"bookpressure-deep": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultBookPressureConfig()
			cfg.MinPressure = 0.75
			cfg.CooldownSeconds = 120
			cfg.MinBidSize = 1000
			cfg.MinAskSize = 1000
			cfg.TakeProfitCents = 4
			cfg.StopLossCents = 2
			cfg.HoldSeconds = 180
			cfg.Label = "bookpressure-deep"
			return algorithms.NewBookPressureStrategy(em, log, cfg)
		},
		"bookpressure-elite": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultBookPressureConfig()
			cfg.MinPressure = 0.80
			cfg.CooldownSeconds = 180
			cfg.MinBidSize = 2000
			cfg.MinAskSize = 2000
			cfg.TakeProfitCents = 3
			cfg.StopLossCents = 2
			cfg.HoldSeconds = 180
			cfg.Label = "bookpressure-elite"
			return algorithms.NewBookPressureStrategy(em, log, cfg)
		},
		// RQ3: series-tier stratification of fadelongshot
		"fadelongshot-itf": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-itf"
			cfg.SeriesFilter = []string{"KXITFMATCH", "KXITFWMATCH", "KXITFDOUBLES", "KXITFWDOUBLES"}
			return algorithms.NewFadeLongshotStrategy(em, log, cfg)
		},
		"fadelongshot-challenger": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-challenger"
			cfg.SeriesFilter = []string{"KXATPCHALLENGERMATCH", "KXWTACHALLENGERMATCH"}
			return algorithms.NewFadeLongshotStrategy(em, log, cfg)
		},
		"fadelongshot-atp": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-atp"
			cfg.SeriesFilter = []string{"KXATPMATCH", "KXATPDOUBLES"}
			return algorithms.NewFadeLongshotStrategy(em, log, cfg)
		},
		"fadelongshot-wta": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-wta"
			cfg.SeriesFilter = []string{"KXWTAMATCH", "KXWTADOUBLES"}
			return algorithms.NewFadeLongshotStrategy(em, log, cfg)
		},
		// RQ13: doubles-only fadelongshot
		"fadelongshot-doubles": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-doubles"
			cfg.SeriesFilter = []string{"KXATPDOUBLES", "KXWTADOUBLES", "KXITFDOUBLES", "KXITFWDOUBLES"}
			return algorithms.NewFadeLongshotStrategy(em, log, cfg)
		},
		// RQ10: US evening only (UTC 18-04)
		"fadelongshot-evening": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-evening"
			cfg.UTCHourStart = 18
			cfg.UTCHourEnd = 4
			return algorithms.NewFadeLongshotStrategy(em, log, cfg)
		},
		// Set-winner prediction: Markov match-win prob + per-set psychological adjustment
		"setwinner": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			s := algorithms.NewSetWinnerStrategy(em, log, algorithms.DefaultSetWinnerConfig())
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		"setwinner-aggro": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultSetWinnerConfig()
			cfg.MinEdgeCents = 1
			cfg.MaxMarketPrice = 0.95
			cfg.CooldownPoints = 1
			cfg.Label = "setwinner-aggro"
			s := algorithms.NewSetWinnerStrategy(em, log, cfg)
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
		// Ablation: pure Markov, no per-set adjustment
		"setwinner-noadjust": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
			cfg := algorithms.DefaultSetWinnerConfig()
			cfg.ReversalPenalty = 0
			cfg.DecidingSetBoost = 0
			cfg.Label = "setwinner-noadjust"
			s := algorithms.NewSetWinnerStrategy(em, log, cfg)
			s.SetSharedMarkovModel(sharedMarkov)
			return s
		},
	// DEEP_RESEARCH_2: setdown filtered to positive-P&L series only.
	"setdown-series": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetDownConfig()
		cfg.Label = "setdown-series"
		cfg.SeriesFilter = []string{"KXATPCHALLENGERMATCH", "KXATPMATCH", "KXWTAMATCH", "KXITFDOUBLES"}
		return algorithms.NewSetDownStrategy(em, log, cfg)
	},
	// DEEP_RESEARCH_2: setdown at UTC 11-13 (noon window, Sharpe 1.17).
	"setdown-noon": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetDownConfig()
		cfg.Label = "setdown-noon"
		cfg.UTCHourStart = 11
		cfg.UTCHourEnd = 13
		return algorithms.NewSetDownStrategy(em, log, cfg)
	},
	// DEEP_RESEARCH_2: tiebreak on ITF women's doubles (Sharpe 2.07, 99.3% hit).
	"tiebreak-itfwdoubles": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultTiebreakConfig()
		cfg.Label = "tiebreak-itfwdoubles"
		cfg.SeriesFilter = []string{"KXITFWDOUBLES"}
		return algorithms.NewTiebreakStrategy(em, log, cfg)
	},
	// DEEP_RESEARCH_2: tiebreak EU daytime only (UTC 10-16).
	"tiebreak-eu-daytime": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultTiebreakConfig()
		cfg.Label = "tiebreak-eu-daytime"
		cfg.UTCHourStart = 10
		cfg.UTCHourEnd = 16
		return algorithms.NewTiebreakStrategy(em, log, cfg)
	},
	// DEEP_RESEARCH_2: cross-arb-favorite on ITF men's only (Sharpe 1.18).
	"cross-arb-favorite-itf": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultCrossArbFavoriteConfig()
		cfg.Label = "cross-arb-favorite-itf"
		cfg.SeriesFilter = []string{"KXITFMATCH"}
		return algorithms.NewCrossArbFavoriteStrategy(em, log, cfg)
	},
	// DEEP_RESEARCH_2: setpoint set 1 only (removes losing set 2+ bucket).
	"setpoint-set1": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.Label = "setpoint-set1"
		cfg.MaxSetNumber = 1
		s := algorithms.NewSetPointStrategy(em, log, cfg)
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// Per-set research: set 1 + mid price [0.20,0.60]. Live data shows
	// set 1 is the winning bucket (+$2978) but <0.20 loses (-$96).
	// Mid-price captures the sweet spot.
	"setpoint-set1-mid": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.Label = "setpoint-set1-mid"
		cfg.MaxSetNumber = 1
		cfg.MinMarketPrice = 0.20
		cfg.MaxMarketPrice = 0.60
		s := algorithms.NewSetPointStrategy(em, log, cfg)
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// Per-set research: set 2 only. Live data shows set 2 overall is
	// slightly negative (-$202) but has structural sub-edges (returning,
	// mid-price). Backtest to see if the set 2 signal exists in replay.
	"setpoint-set2": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.Label = "setpoint-set2"
		cfg.MinSetNumber = 2
		cfg.MaxSetNumber = 2
		s := algorithms.NewSetPointStrategy(em, log, cfg)
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// Per-set research: set 2 + returning only. Live: n=88, 64.8% win,
	// +19.3% edge vs BE, +$163. Serving in set 2 loses (-$365).
	"setpoint-set2-ret": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.Label = "setpoint-set2-ret"
		cfg.MinSetNumber = 2
		cfg.MaxSetNumber = 2
		cfg.IncludeServing = false
		s := algorithms.NewSetPointStrategy(em, log, cfg)
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// Per-set research: sets 1-2 + mid price [0.20,0.60]. Combines the
	// two winning per-set buckets while excluding <0.20 (loses in both
	// sets) and 0.60-0.80 (loses badly in set 2, -$559).
	"setpoint-set12-mid": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultSetPointConfig()
		cfg.Label = "setpoint-set12-mid"
		cfg.MaxSetNumber = 2
		cfg.MinMarketPrice = 0.20
		cfg.MaxMarketPrice = 0.60
		s := algorithms.NewSetPointStrategy(em, log, cfg)
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// DEEP_RESEARCH_2: convexpool on WTA only (Sharpe 0.39 vs 0.12 base).
	"convexpool-wta": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		cfg := algorithms.DefaultConvexPoolConfig()
		cfg.Label = "convexpool-wta"
		cfg.SeriesFilter = []string{"KXWTAMATCH"}
		s := algorithms.NewConvexPoolStrategy(em, log, cfg)
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// Convexpool with full sell-to-close pipeline (TP/SL/time + edge reversal).
	"convexpool-exit": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		s := algorithms.NewConvexPoolExitStrategy(em, log, algorithms.DefaultConvexPoolExitConfig())
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	// Convexpool with dynamic alpha scaling with score depth.
	"convexpool-adaptive": func(em algorithms.OrderEmitter, log *slog.Logger) ReplayStrategy {
		s := algorithms.NewConvexPoolAdaptiveStrategy(em, log, algorithms.DefaultConvexPoolAdaptiveConfig())
		s.SetSharedMarkovModel(sharedMarkov)
		return s
	},
	}
}
