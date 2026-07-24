package strategies

import (
	"log/slog"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	sigpkg "github.com/farquaad/kalshi-ghost-trader/internal/signal"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Build creates the multi-strategy runtime with all registered strategies.
func Build(emitter algorithms.OrderEmitter, db *store.DB, log *slog.Logger) *algorithms.MultiStrategyRuntime {
	matchPoint := algorithms.NewMatchPointStrategy(emitter, log)

	fadeLongshot := algorithms.NewFadeLongshotStrategyWithDB(emitter, db, log,
		algorithms.DefaultFadeLongshotConfig())

	noFade := algorithms.NewNoFadeStrategyWithDB(emitter, db, log,
		algorithms.DefaultNoFadeConfig())

	// R.8: One shared Markov model for all strategies using pServe=0.64.
	// Memoization works across strategies — same score state computed once
	// instead of N times. Model is mutex-guarded; safe for concurrent use.
	// Strategies with different pServe (calibrated-markov, surface-markov)
	// keep their own per-call models.
	sharedMarkov := algorithms.NewMarkovModel()

	// Capture Markov-using strategies to inject the shared model after
	// the factory map builds them.
	var bp *algorithms.BreakPointStrategy
	var cp, cpWTA *algorithms.ConvexPoolStrategy
	var cpExit *algorithms.ConvexPoolExitStrategy
	var cpAdaptive *algorithms.ConvexPoolAdaptiveStrategy
	var sw, swAggro, swNoAdj *algorithms.SetWinnerStrategy
	var sp, spServe, spCheap, spSet1, spAggro, spSet1Mid, spSet2, spSet2Ret, spSet12Mid *algorithms.SetPointStrategy

	multi := algorithms.NewMultiStrategyFromFactories(emitter, log, map[string]algorithms.StrategyFactoryFn{
		"matchpoint": func(e algorithms.OrderEmitter) algorithms.Strategy { return matchPoint },
		"matchpoint-aggro": func(e algorithms.OrderEmitter) algorithms.Strategy {
			spAggro = algorithms.NewSetPointStrategy(e, log, algorithms.SetPointConfig{
				IncludeSetPoints: false,
				IncludeReturning: true,
				IncludeServing:   true,
				PServe:           0.64,
				MinMarketPrice:   0.05,
				MinEdgeCents:     5,
				CooldownPoints:   3,
				Label:            "matchpoint-aggro",
			})
			return spAggro
		},
		"setpoint": func(e algorithms.OrderEmitter) algorithms.Strategy {
			sp = algorithms.NewSetPointStrategyWithDB(e, db, log, algorithms.DefaultSetPointConfig())
			return sp
		},
		"setpoint-serve": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.IncludeReturning = false
			cfg.Label = "setpoint-serve"
			spServe = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spServe
		},
		"setpoint-cheap": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.MaxMarketPrice = 0.50
			cfg.Label = "setpoint-cheap"
			spCheap = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spCheap
		},
		"fadelongshot": func(e algorithms.OrderEmitter) algorithms.Strategy { return fadeLongshot },
		"nofade":       func(e algorithms.OrderEmitter) algorithms.Strategy { return noFade },
		"breakback": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewBreakBackStrategy(e, log, algorithms.DefaultBreakBackConfig())
		},
		"setdown": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSetDownStrategy(e, log, algorithms.DefaultSetDownConfig())
		},
		"server1530": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewServer1530Strategy(e, log, algorithms.DefaultServer1530Config())
		},
		"tiebreak": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewTiebreakStrategy(e, log, algorithms.DefaultTiebreakConfig())
		},
		"breakpoint": func(e algorithms.OrderEmitter) algorithms.Strategy {
			bp = algorithms.NewBreakPointStrategy(e, log, algorithms.DefaultBreakPointConfig())
			return bp
		},
		"adout": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewAdOutStrategy(e, log, algorithms.DefaultAdOutConfig())
		},
		"convexpool": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cp = algorithms.NewConvexPoolStrategy(e, log, algorithms.DefaultConvexPoolConfig())
			return cp
		},
		"comeback040": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewComeback040Strategy(e, log, algorithms.DefaultComeback040Config())
		},
		"calibrated-markov": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewCalibratedMarkovStrategyWithDB(e, db, log, algorithms.DefaultCalibratedMarkovConfig())
		},
		"cross-arb": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewCrossArbStrategy(e, log, algorithms.DefaultCrossArbConfig())
		},
		"cross-arb-favorite": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewCrossArbFavoriteStrategy(e, log, algorithms.DefaultCrossArbFavoriteConfig())
		},
		"tiebreak-server": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewTiebreakServerStrategy(e, log, algorithms.DefaultTiebreakServerConfig())
		},
		"set1winner": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSet1WinnerStrategy(e, log, algorithms.DefaultSet1WinnerConfig())
		},
		"volratio": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewVolumeRatioStrategyWithDB(e, db, log, algorithms.DefaultVolumeRatioConfig())
		},
		"surface-markov": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSurfaceMarkovStrategyWithDB(e, db, log, algorithms.DefaultSurfaceMarkovConfig())
		},
		"spike-fade": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSpikeFadeStrategy(e, log, algorithms.DefaultSpikeFadeConfig())
		},
		"buythedip": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewBuyTheDipStrategy(e, log, algorithms.DefaultBuyTheDipConfig())
		},
		"fadelongshot-itf": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-itf"
			cfg.SeriesFilter = []string{"KXITFMATCH", "KXITFWMATCH", "KXITFDOUBLES", "KXITFWDOUBLES"}
			return algorithms.NewFadeLongshotStrategyWithDB(e, db, log, cfg)
		},
		"fadelongshot-challenger": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-challenger"
			cfg.SeriesFilter = []string{"KXATPCHALLENGERMATCH", "KXWTACHALLENGERMATCH"}
			return algorithms.NewFadeLongshotStrategyWithDB(e, db, log, cfg)
		},
		"fadelongshot-atp": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-atp"
			cfg.SeriesFilter = []string{"KXATPMATCH", "KXATPDOUBLES"}
			return algorithms.NewFadeLongshotStrategyWithDB(e, db, log, cfg)
		},
		"fadelongshot-wta": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-wta"
			cfg.SeriesFilter = []string{"KXWTAMATCH", "KXWTADOUBLES"}
			return algorithms.NewFadeLongshotStrategyWithDB(e, db, log, cfg)
		},
		"fadelongshot-doubles": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-doubles"
			cfg.SeriesFilter = []string{"KXATPDOUBLES", "KXWTADOUBLES", "KXITFDOUBLES", "KXITFWDOUBLES"}
			return algorithms.NewFadeLongshotStrategyWithDB(e, db, log, cfg)
		},
		"fadelongshot-evening": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultFadeLongshotConfig()
			cfg.Label = "fadelongshot-evening"
			cfg.UTCHourStart = 18
			cfg.UTCHourEnd = 4
			return algorithms.NewFadeLongshotStrategyWithDB(e, db, log, cfg)
		},
		// DEEP_RESEARCH_2: filtered variants of existing winners.
		"setdown-series": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetDownConfig()
			cfg.Label = "setdown-series"
			cfg.SeriesFilter = []string{"KXATPCHALLENGERMATCH", "KXATPMATCH", "KXWTAMATCH", "KXITFDOUBLES"}
			return algorithms.NewSetDownStrategyWithDB(e, db, log, cfg)
		},
		"setdown-noon": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetDownConfig()
			cfg.Label = "setdown-noon"
			cfg.UTCHourStart = 11
			cfg.UTCHourEnd = 13
			return algorithms.NewSetDownStrategyWithDB(e, db, log, cfg)
		},
		"tiebreak-itfwdoubles": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultTiebreakConfig()
			cfg.Label = "tiebreak-itfwdoubles"
			cfg.SeriesFilter = []string{"KXITFWDOUBLES"}
			return algorithms.NewTiebreakStrategyWithDB(e, db, log, cfg)
		},
		"tiebreak-eu-daytime": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultTiebreakConfig()
			cfg.Label = "tiebreak-eu-daytime"
			cfg.UTCHourStart = 10
			cfg.UTCHourEnd = 16
			return algorithms.NewTiebreakStrategyWithDB(e, db, log, cfg)
		},
		"cross-arb-favorite-itf": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultCrossArbFavoriteConfig()
			cfg.Label = "cross-arb-favorite-itf"
			cfg.SeriesFilter = []string{"KXITFMATCH"}
			return algorithms.NewCrossArbFavoriteStrategyWithDB(e, db, log, cfg)
		},
		"setpoint-set1": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.Label = "setpoint-set1"
			cfg.MaxSetNumber = 1
			spSet1 = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spSet1
		},
		"setpoint-set1-mid": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.Label = "setpoint-set1-mid"
			cfg.MaxSetNumber = 1
			cfg.MinMarketPrice = 0.20
			cfg.MaxMarketPrice = 0.60
			spSet1Mid = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spSet1Mid
		},
		"setpoint-set2": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.Label = "setpoint-set2"
			cfg.MinSetNumber = 2
			cfg.MaxSetNumber = 2
			spSet2 = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spSet2
		},
		"setpoint-set2-ret": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.Label = "setpoint-set2-ret"
			cfg.MinSetNumber = 2
			cfg.MaxSetNumber = 2
			cfg.IncludeServing = false
			spSet2Ret = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spSet2Ret
		},
		"setpoint-set12-mid": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetPointConfig()
			cfg.Label = "setpoint-set12-mid"
			cfg.MaxSetNumber = 2
			cfg.MinMarketPrice = 0.20
			cfg.MaxMarketPrice = 0.60
			spSet12Mid = algorithms.NewSetPointStrategyWithDB(e, db, log, cfg)
			return spSet12Mid
		},
		"convexpool-wta": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultConvexPoolConfig()
			cfg.Label = "convexpool-wta"
			cfg.SeriesFilter = []string{"KXWTAMATCH"}
			cpWTA = algorithms.NewConvexPoolStrategyWithDB(e, db, log, cfg)
			return cpWTA
		},
		"convexpool-exit": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cpExit = algorithms.NewConvexPoolExitStrategyWithDB(e, db, log, algorithms.DefaultConvexPoolExitConfig())
			return cpExit
		},
		"convexpool-adaptive": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cpAdaptive = algorithms.NewConvexPoolAdaptiveStrategyWithDB(e, db, log, algorithms.DefaultConvexPoolAdaptiveConfig())
			return cpAdaptive
		},
		"doublebreak": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewDoubleBreakStrategy(e, log, algorithms.DefaultDoubleBreakConfig())
		},
		"bookpressure": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewBookPressureStrategy(e, log, algorithms.DefaultBookPressureConfig())
		},
		"bookpressure-strict": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultBookPressureConfig()
			cfg.MinPressure = 0.70
			cfg.CooldownSeconds = 180
			cfg.MinBidSize = 500
			cfg.MinAskSize = 500
			cfg.TakeProfitCents = 3
			cfg.StopLossCents = 2
			cfg.Label = "bookpressure-strict"
			return algorithms.NewBookPressureStrategy(e, log, cfg)
		},
		"bookpressure-deep": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultBookPressureConfig()
			cfg.MinPressure = 0.75
			cfg.CooldownSeconds = 120
			cfg.MinBidSize = 1000
			cfg.MinAskSize = 1000
			cfg.TakeProfitCents = 4
			cfg.StopLossCents = 2
			cfg.HoldSeconds = 180
			cfg.Label = "bookpressure-deep"
			return algorithms.NewBookPressureStrategy(e, log, cfg)
		},
		"bookpressure-elite": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultBookPressureConfig()
			cfg.MinPressure = 0.80
			cfg.CooldownSeconds = 180
			cfg.MinBidSize = 2000
			cfg.MinAskSize = 2000
			cfg.TakeProfitCents = 3
			cfg.StopLossCents = 2
			cfg.HoldSeconds = 180
			cfg.Label = "bookpressure-elite"
			return algorithms.NewBookPressureStrategy(e, log, cfg)
		},
		"setwinner": func(e algorithms.OrderEmitter) algorithms.Strategy {
			sw = algorithms.NewSetWinnerStrategy(e, log, algorithms.DefaultSetWinnerConfig())
			return sw
		},
		"setwinner-aggro": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetWinnerConfig()
			cfg.MinEdgeCents = 1
			cfg.MaxMarketPrice = 0.95
			cfg.CooldownPoints = 1
			cfg.Label = "setwinner-aggro"
			swAggro = algorithms.NewSetWinnerStrategy(e, log, cfg)
			return swAggro
		},
		"setwinner-noadjust": func(e algorithms.OrderEmitter) algorithms.Strategy {
			cfg := algorithms.DefaultSetWinnerConfig()
			cfg.ReversalPenalty = 0
			cfg.DecidingSetBoost = 0
			cfg.Label = "setwinner-noadjust"
			swNoAdj = algorithms.NewSetWinnerStrategy(e, log, cfg)
			return swNoAdj
		},
		"close_timer": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return sigpkg.NewCloseTimer(db, matchPoint, e, log)
		},
	})

	// R.8: Inject shared Markov model into all pServe=0.64 strategies.
	if bp != nil {
		bp.SetSharedMarkovModel(sharedMarkov)
	}
	if cp != nil {
		cp.SetSharedMarkovModel(sharedMarkov)
	}
	if cpWTA != nil {
		cpWTA.SetSharedMarkovModel(sharedMarkov)
	}
	if cpExit != nil {
		cpExit.SetSharedMarkovModel(sharedMarkov)
	}
	if cpAdaptive != nil {
		cpAdaptive.SetSharedMarkovModel(sharedMarkov)
	}
	if sw != nil {
		sw.SetSharedMarkovModel(sharedMarkov)
	}
	if swAggro != nil {
		swAggro.SetSharedMarkovModel(sharedMarkov)
	}
	if swNoAdj != nil {
		swNoAdj.SetSharedMarkovModel(sharedMarkov)
	}
	if sp != nil {
		sp.SetSharedMarkovModel(sharedMarkov)
	}
	if spServe != nil {
		spServe.SetSharedMarkovModel(sharedMarkov)
	}
	if spCheap != nil {
		spCheap.SetSharedMarkovModel(sharedMarkov)
	}
	if spSet1 != nil {
		spSet1.SetSharedMarkovModel(sharedMarkov)
	}
	if spAggro != nil {
		spAggro.SetSharedMarkovModel(sharedMarkov)
	}

	multi.SetDB(db)

	return multi
}
