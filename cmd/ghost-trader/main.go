// Command ghost-trader is the main entrypoint for the Kalshi Ghost Trader service.
//
// It loads configuration from the SQLite database (app_config table), initializes
// an RSA signer for Kalshi API authentication, opens a SQLite database with WAL
// mode, and launches all core goroutines via errgroup:
//
//   - TickWriter — single SQLite writer for batched tick/orderbook/lifecycle/points inserts
//   - WebSocket Manager — auto-reconnecting connection to Kalshi's real-time feed
//   - Scanner — periodic REST scan for new tennis events and markets
//   - Scheduler — starts per-market WS tracking at occurrence_datetime minus lead time
//   - Metrics Server — runtime stats + pprof on 127.0.0.1 (default port 6060)
//
// SIGINT/SIGTERM triggers graceful shutdown: root context is cancelled, errgroup
// waits for all goroutines to exit, tracker unsubscribes all markets, and the
// database is closed after the TickWriter has flushed remaining batches.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/apitennis"
	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiauth"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshilivedata"
	"github.com/farquaad/kalshi-ghost-trader/internal/orderbackfill"
	"github.com/farquaad/kalshi-ghost-trader/internal/reconciler"
	"github.com/farquaad/kalshi-ghost-trader/internal/scanner"
	"github.com/farquaad/kalshi-ghost-trader/internal/schedulechecker"
	"github.com/farquaad/kalshi-ghost-trader/internal/scheduler"
	sigpkg "github.com/farquaad/kalshi-ghost-trader/internal/signal"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
	wsclient "github.com/farquaad/kalshi-ghost-trader/internal/ws"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(time.RFC3339))
				}
			}
			return a
		},
	}))
	slog.SetDefault(log)

	// Bootstrap: DB path from env, open SQLite, load config from app_config table
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "kalshi_tennis.db"
	}

	// Root context cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Open SQLite store
	db, err := store.New(ctx, dbPath, log)
	if err != nil {
		log.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Load config from DB
	cfg, err := config.LoadFromDB(db)
	if err != nil {
		log.Error("config load from DB failed", "err", err)
		os.Exit(1)
	}
	_ = config.NewConfigCache(db, cfg) // used in Phase 3 for real order pipeline
	algorithms.SetSizingParams(cfg.PaperBankroll, cfg.KellyFraction)
	algorithms.SetRealBankroll(cfg.RealBankroll)
	log.Info("config loaded from DB", "env", cfg.Environment, "db", cfg.DBPath, "series_count", len(cfg.SeriesTickers), "paper_bankroll", cfg.PaperBankroll, "real_bankroll", cfg.RealBankroll, "kelly", cfg.KellyFraction)

	// Load signer
	signer, err := kalshiauth.NewSignerFromFile(cfg.APIKeyID, cfg.PrivateKeyPath)
	if err != nil {
		log.Error("signer init failed", "err", err)
		os.Exit(1)
	}

	// Tick writer (single writer goroutine for batch inserts)
	tickWriter := db.NewTickWriter(cfg.BatchSize, cfg.FlushTimeoutMS, log)

	// REST client
	restClient := kalshiclient.NewClient(cfg.RESTBaseURL, signer,
		time.Duration(cfg.HTTPTimeoutSecs)*time.Second, cfg.RateLimitRPS, log)

	// Order emission pipeline:
	//   strategies → paperGuard → paperEmitter (TickWriter, ALWAYS)
	//                    ↓ (inner, if paper quota approved)
	//                 realGuard → KalshiOrderEmitter (if real_trading_enabled)
	//                    ↓ (if real quota approved)
	//                 NoopEmitter (if real trading disabled)
	paperEmitter := algorithms.NewTickWriterEmitter(tickWriter)

	// Paper guard — always active. When quota disabled, passes all through.
	paperGuard := algorithms.NewQuotaGuard(paperEmitter, algorithms.NoopEmitter{}, algorithms.QuotaConfig{
		Enabled:      cfg.OrderQuotaEnabled,
		CooldownSecs: cfg.OrderQuotaCooldownSecs,
		MaxPerSec:    cfg.OrderQuotaMaxPerSec,
		BudgetTotal:  cfg.OrderQuotaBudgetTotal,
		BudgetFloor:  cfg.OrderQuotaBudgetFloor,
	}, log)
	defer paperGuard.Close()

	// Real order pipeline — only wired when real_trading_enabled
	var realGuard *algorithms.QuotaGuard
	if cfg.RealTradingEnabled {
		log.Warn("REAL TRADING ENABLED — live orders will be submitted to Kalshi",
			"environment", cfg.Environment, "bankroll", cfg.RealBankroll)

		// Auto-init liquidity pool if not yet seeded and bankroll is meaningful
		if cfg.RealBankroll > 1 {
			if _, err := db.GetLiquidityPool(ctx); err != nil {
				initialCents := int64(cfg.RealBankroll * 100)
				if err := db.InitLiquidityPool(ctx, initialCents); err != nil {
					log.Error("auto-init liquidity pool", "err", err)
				} else {
					log.Info("auto-initialized liquidity pool", "initial_cents", initialCents)
				}
			}
		}

		realEmitter := algorithms.NewKalshiOrderEmitter(restClient, db, algorithms.RealOrderConfig{
			Enabled:       true,
			Bankroll:      cfg.RealBankroll,
			Environment:   cfg.Environment,
			TimeInForce:   cfg.RealOrderTimeInForce,
			OrderTimeoutS: cfg.RealOrderTimeoutS,
		}, log)

		realGuard = algorithms.NewQuotaGuard(algorithms.NoopEmitter{}, realEmitter, algorithms.QuotaConfig{
			Enabled:      cfg.OrderQuotaEnabled,
			CooldownSecs: cfg.OrderQuotaCooldownSecs,
			MaxPerSec:    cfg.OrderQuotaMaxPerSec,
			BudgetTotal:  cfg.OrderQuotaBudgetTotal,
			BudgetFloor:  cfg.OrderQuotaBudgetFloor,
		}, log)
		defer realGuard.Close()

		// Wire realGuard as inner of paperGuard
		paperGuard.SetInner(realGuard)
	}

	// matchPoint is the primary strategy and also serves as PriceLookup for CloseTimer
	matchPoint := algorithms.NewMatchPointStrategy(paperGuard, log)

	// FadeLongshot: buy favorite at T-10min before close. Highest Sharpe (1.01).
	// Created outside factory because it needs DB for live close_ts loading.
	// All orders are paper trades — TickWriterEmitter writes to orders table, no real execution.
	fadeLongshot := algorithms.NewFadeLongshotStrategyWithDB(paperGuard, db, log,
		algorithms.DefaultFadeLongshotConfig())

	noFade := algorithms.NewNoFadeStrategyWithDB(paperGuard, db, log,
		algorithms.DefaultNoFadeConfig())

	// Multi-strategy runtime: all point-based strategies run simultaneously.
	// Each strategy's orders are tagged with its name in the orders table.
	multi := algorithms.NewMultiStrategyFromFactories(paperGuard, log, map[string]algorithms.StrategyFactoryFn{
		"matchpoint": func(e algorithms.OrderEmitter) algorithms.Strategy { return matchPoint },
		"matchpoint-aggro": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSetPointStrategy(e, log, algorithms.SetPointConfig{
				IncludeSetPoints: false,
				IncludeReturning: true,
				ServeConvProb:    0.97,
				ReturnConvProb:   0.89,
				MinMarketPrice:   0.05,
				MinEdgeCents:     1,
				Label:            "matchpoint-aggro",
			})
		},
		"setpoint": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSetPointStrategy(e, log, algorithms.SetPointConfig{
				IncludeSetPoints: true,
				IncludeReturning: true,
				ServeConvProb:    0.93,
				ReturnConvProb:   0.89,
				MinMarketPrice:   0.05,
				MinEdgeCents:     1,
				Label:            "setpoint",
			})
		},
		"setpoint-serve": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSetPointStrategy(e, log, algorithms.SetPointConfig{
				IncludeSetPoints: true,
				IncludeReturning: false,
				ServeConvProb:    0.93,
				ReturnConvProb:   0.89,
				MinMarketPrice:   0.05,
				MinEdgeCents:     1,
				Label:            "setpoint-serve",
			})
		},
		"setpoint-cheap": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewSetPointStrategy(e, log, algorithms.SetPointConfig{
				IncludeSetPoints: true,
				IncludeReturning: true,
				ServeConvProb:    0.93,
				ReturnConvProb:   0.89,
				MaxMarketPrice:   0.50,
				MinMarketPrice:   0.05,
				MinEdgeCents:     1,
				Label:            "setpoint-cheap",
			})
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
			return algorithms.NewBreakPointStrategy(e, log, algorithms.DefaultBreakPointConfig())
		},
		"convexpool": func(e algorithms.OrderEmitter) algorithms.Strategy {
			return algorithms.NewConvexPoolStrategy(e, log, algorithms.DefaultConvexPoolConfig())
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
	})
	multi.SetDB(db)
	log.Info("multi-strategy runtime initialized", "strategies", multi.String())

	// Close timer strategy (buy favorite N min before close)
	var closeTimer *sigpkg.CloseTimer
	if cfg.CloseTimerEnabled {
		closeTimer = sigpkg.NewCloseTimer(db, matchPoint, tickWriter,
			cfg.CloseTimerLeadMin, cfg.CloseTimerMinPrice, log)
		log.Info("close timer strategy enabled",
			"lead_min", cfg.CloseTimerLeadMin,
			"min_price", cfg.CloseTimerMinPrice)
	}

	// WebSocket manager
	wsMgr := wsclient.NewManager(cfg.WSURL, signer, tickWriter, cfg.SeriesTickers,
		time.Duration(cfg.WSMinBackoffSecs)*time.Second,
		time.Duration(cfg.WSMaxBackoffSecs)*time.Second,
		log)
	wsMgr.SetPriceUpdater(multi)

	// API-Tennis scraper (optional — WebSocket real-time push, no polling delay)
	var atScraper *apitennis.Scraper
	if cfg.APITennisEnabled {
		if cfg.APITennisAPIKey == "" {
			log.Error("apitennis_enabled but apitennis_api_key is empty")
			os.Exit(1)
		}
		atScraper = apitennis.New(db, multi, tickWriter, cfg.APITennisAPIKey,
			cfg.APITennisTimezone, log)
		log.Info("apitennis scraper enabled", "timezone", cfg.APITennisTimezone)
	}

	// Kalshi live-data poller (optional — backup score source via REST polling)
	var kldPoller *kalshilivedata.Poller
	if cfg.KalshiLiveDataEnabled {
		kldPoller = kalshilivedata.New(restClient, db, multi, tickWriter,
			time.Duration(cfg.KalshiLiveDataPollSecs)*time.Second, log)
		log.Info("kalshi livedata poller enabled", "poll_secs", cfg.KalshiLiveDataPollSecs)
	}

	// Tracker (market subscription lifecycle)
	// Score poller coupling: tracker drives polling on subscribe/unsubscribe.
	// Both API-Tennis (primary) and Kalshi live-data (backup) are wired.
	var scorePoller tracker.ScorePoller
	var pollers []tracker.ScorePoller
	if atScraper != nil {
		pollers = append(pollers, atScraper)
	}
	if kldPoller != nil {
		pollers = append(pollers, kldPoller)
	}
	if len(pollers) == 1 {
		scorePoller = pollers[0]
	} else if len(pollers) > 1 {
		scorePoller = tracker.NewMultiScorePoller(pollers...)
	}
	tr := tracker.New(wsMgr, scorePoller, log)
	tr.SetPriceCleaner(multi)
	tr.SetMarketRegistrar(multi)

	// Scanner
	sc := scanner.New(restClient, db, cfg.SeriesTickers, log)

	// Scheduler
	sched := scheduler.New(db, tr, cfg.TrackLeadMinutes, log)

	// errgroup for top-level goroutines
	g, ctx := errgroup.WithContext(ctx)

	// Backtest engine for dashboard strategy API
	btEngine, err := backtest.NewEngine(cfg.DBPath, log)
	if err != nil {
		log.Error("backtest engine init failed", "err", err)
		os.Exit(1)
	}
	defer btEngine.Close()

	// Backtest result cache — 5 min TTL
	btCache := backtest.NewCache(5 * time.Minute)

	// pprof + runtime metrics + strategy API server
	if cfg.MetricsPort > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/metrics", metricsHandler)
		mux.HandleFunc("/api/tracked", trackedHandler(tr, btEngine))
		mux.HandleFunc("/api/strategies", corsHandler(strategyListHandler(btEngine)))
		mux.HandleFunc("/api/backtest", corsHandler(backtestHandler(btEngine, btCache, log)))
		mux.HandleFunc("/api/price-bands", corsHandler(priceBandsHandler(btEngine, log)))
		mux.HandleFunc("/api/ticks", corsHandler(ticksHandler(btEngine, log)))
		mux.HandleFunc("/api/orders", corsHandler(ordersHandler(btEngine, log)))
		mux.HandleFunc("/api/order-counts", corsHandler(orderCountsHandler(btEngine, log)))
		mux.HandleFunc("/api/pending-order-counts", corsHandler(pendingOrderCountsHandler(btEngine, log)))
		mux.HandleFunc("/api/passed-matches", corsHandler(passedMatchesHandler(btEngine, log)))
		mux.HandleFunc("/api/real-orders", corsHandler(realOrdersHandler(db, log)))
		mux.HandleFunc("/api/liquidity-pool", corsHandler(liquidityPoolHandler(db, log)))
		mux.HandleFunc("/api/strategy-config", corsHandler(strategyConfigHandler(db, log)))
		mux.HandleFunc("/api/trigger-ranges", corsHandler(triggerRangesHandler(db, log)))
		mux.HandleFunc("/api/app-config", corsHandler(appConfigHandler(db, log)))
		mux.Handle("/debug/pprof/", http.DefaultServeMux)
		metricsSrv := &http.Server{
			Addr:         fmt.Sprintf("127.0.0.1:%d", cfg.MetricsPort),
			Handler:      corsMiddleware(mux),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 120 * time.Second,
		}
		g.Go(func() error {
			log.Info("metrics server listening", "addr", metricsSrv.Addr)
			err := metricsSrv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		})
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			metricsSrv.Shutdown(shutdownCtx)
		}()
	}

	// 1. Tick writer (single SQLite writer)
	g.Go(func() error {
		return tickWriter.Run(ctx)
	})

	// 2. WebSocket manager (auto-reconnect, dispatch)
	g.Go(func() error {
		return wsMgr.Run(ctx)
	})

	// 3. Scanner loop (daily scan for new matches)
	scanInterval := time.Duration(cfg.ScanIntervalHours) * time.Hour
	g.Go(func() error {
		return sc.RunLoop(ctx, scanInterval)
	})

	// 4. Scheduler loop (poll DB, schedule tracking at occurrence_datetime - lead)
	schedPoll := time.Duration(cfg.SchedulerPollSecs) * time.Second
	g.Go(func() error {
		return sched.Run(ctx, schedPoll)
	})

	// 4b. Reconciler loop (fill settlement gaps via REST for unresolved markets)
	recon := reconciler.New(restClient, db, log)
	reconInterval := time.Duration(cfg.ReconcilerIntervalSecs) * time.Second
	g.Go(func() error {
		return recon.Run(ctx, reconInterval)
	})

	// 4c. Order backfill loop (refresh stale real order status from REST)
	obf := orderbackfill.New(restClient, db, log)
	obfInterval := time.Duration(cfg.OrderBackfillIntervalSecs) * time.Second
	g.Go(func() error {
		return obf.Run(ctx, obfInterval)
	})

	// 4d. Schedule checker loop (refresh stale occurrence_ts from REST)
	schedChk := schedulechecker.New(restClient, db, multi, log)
	schedChkInterval := time.Duration(cfg.ScheduleCheckerIntervalSecs) * time.Second
	g.Go(func() error {
		return schedChk.Run(ctx, schedChkInterval)
	})

	// 5. API-Tennis scraper goroutine (optional — WS real-time push)
	if atScraper != nil {
		g.Go(func() error {
			return atScraper.Run(ctx)
		})
	}

	// 5b. Kalshi live-data poller goroutine (optional — backup score source)
	// Per-match goroutines are launched by StartPolling via tracker; this
	// just blocks until ctx cancelled for clean shutdown.
	if kldPoller != nil {
		g.Go(func() error {
			return kldPoller.Run(ctx)
		})
	}

	// 6. Close timer strategy goroutine (optional — buys favorites near close)
	if cfg.CloseTimerEnabled {
		g.Go(func() error {
			return closeTimer.Run(ctx, cfg.CloseTimerPollSecs)
		})
	}

	log.Info("ghost trader running", "scan_interval", scanInterval, "lead_minutes", cfg.TrackLeadMinutes)

	// Wait for shutdown signal or critical failure
	err = g.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("shutdown error", "err", err)
	}

	// Orderly teardown
	tr.StopAll()
	log.Info("clean shutdown complete")
}
