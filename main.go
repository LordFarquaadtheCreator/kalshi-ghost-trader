// Command ghost-trader is the main entrypoint for the Kalshi Ghost Trader service.
//
// It loads configuration from the PostgreSQL database (app_config table), initializes
// an RSA signer for Kalshi API authentication, opens a PostgreSQL database, and
// launches all core goroutines via errgroup:
//
//   - TickWriter — single writer for batched tick/orderbook/lifecycle/points inserts
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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/apitennis"
	"github.com/farquaad/kalshi-ghost-trader/internal/appconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/dashboardapi"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiAuth"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshilivedata"
	"github.com/farquaad/kalshi-ghost-trader/internal/orderbackfill"
	"github.com/farquaad/kalshi-ghost-trader/internal/pricebands"
	"github.com/farquaad/kalshi-ghost-trader/internal/reconciler"
	"github.com/farquaad/kalshi-ghost-trader/internal/scanner"
	"github.com/farquaad/kalshi-ghost-trader/internal/schedulechecker"
	"github.com/farquaad/kalshi-ghost-trader/internal/scheduler"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/strategies"
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

	// Root context cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Open store
	appCfg, err := appconfig.Load()
	if err != nil {
		log.Error("app config load failed", "err", err)
		os.Exit(1)
	}
	db, err := store.New(ctx, appCfg.DBDSN, log)
	if err != nil {
		log.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Error("schema migration failed", "err", err)
		os.Exit(1)
	}

	// Load config
	if _, err := config.Load(db); err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	log.Info("config loaded", "env", config.Cfg.Environment, "db", config.Cfg.DBDSN,
		"series_count", len(config.Cfg.SeriesTickers),
		"paper_bankroll", config.Cfg.PaperBankroll,
		"real_bankroll", config.Cfg.RealBankroll, "kelly", config.Cfg.KellyFraction)

	// Load signer
	signer, err := kalshiAuth.NewSignerFromFile()
	if err != nil {
		log.Error("signer init failed", "err", err)
		os.Exit(1)
	}
	log.Info("signer loaded successfully")

	algorithms.SetSizingParams()
	log.Info("sizing params set", "paper_bankroll", algorithms.GetPaperBankroll(), "kelly_fraction", config.Cfg.KellyFraction)
	algorithms.SetRealBankroll()
	log.Info("real bankroll set", "real_bankroll", config.Cfg.RealBankroll)

	// Tick writer (single writer goroutine for batch inserts)
	tickWriter := db.NewTickWriter(config.Cfg.BatchSize, config.Cfg.FlushTimeoutMS, log)

	// REST client — shared by scanner, livedata pollers, tracker.
	restClient := kalshiclient.NewClient(signer, log)

	// Dedicated REST client for order submission.
	orderClient := kalshiclient.NewClient(signer, log)

	// Order emission pipeline:
	//   strategies → paperQuotaGuard → paperEmitter (TickWriter, ALWAYS)
	//                    ↓ (inner, if paper quota approved)
	//                 realQuotaGuard → KalshiOrderEmitter (if real_trading_enabled)
	//                    ↓ (if real quota approved)
	//                 NoopEmitter (if real trading disabled)
	paperEmitter := algorithms.NewEnrichEmitter(
		algorithms.NewTickWriterEmitter(tickWriter), db, log)

	// Paper quota guard — always active. When quota disabled, passes all through.
	paperQuotaGuard := algorithms.NewQuotaGuard(paperEmitter, algorithms.NoopEmitter{}, log)
	defer paperQuotaGuard.Close()

	// Real order pipeline — always constructed. Live on/off controlled by
	// real_trading_enabled, checked per EmitOrder via liveToggleEmitter.
	// Dashboard flip takes effect on next order without restart.
	if config.Cfg.RealTradingEnabled {
		log.Warn("REAL TRADING ENABLED — live orders will be submitted to Kalshi",
			"environment", config.Cfg.Environment, "bankroll", config.Cfg.RealBankroll)
	}

	realEmitter := algorithms.NewKalshiOrderEmitter(orderClient, db, log)

	realQuotaGuard := algorithms.NewQuotaGuard(algorithms.NoopEmitter{}, realEmitter, log)
	defer realQuotaGuard.Close()

	paperQuotaGuard.SetInner(&algorithms.LiveToggleEmitter{Inner: realQuotaGuard, Log: log})

	multi := strategies.Build(paperQuotaGuard, db, log)
	log.Info("multi-strategy runtime initialized", "strategies", multi.String())

	// WebSocket manager
	wsMgr := wsclient.NewManager(signer, tickWriter, log)
	wsMgr.SetPriceUpdater(multi)

	// API-Tennis scraper (mandatory — WebSocket real-time push, primary score source)
	atScraper := apitennis.New(db, multi, tickWriter, log)
	log.Info("apitennis scraper initialized", "timezone", config.Cfg.APITennisTimezone)

	// Kalshi live-data poller (optional — backup score source via REST polling)
	var kldPoller *kalshilivedata.Poller
	if config.Cfg.KalshiLiveDataEnabled {
		kldPoller = kalshilivedata.New(restClient, db, multi, tickWriter, log)
		log.Info("kalshi livedata poller enabled", "poll_secs", config.Cfg.KalshiLiveDataPollSecs)
	}

	// Tracker (market subscription lifecycle)
	// Score poller coupling: tracker drives polling on subscribe/unsubscribe.
	// API-Tennis (primary) always wired; Kalshi live-data (backup) optional.
	var scorePoller tracker.ScorePoller
	pollers := []tracker.ScorePoller{atScraper}
	if kldPoller != nil {
		pollers = append(pollers, kldPoller)
	}
	if len(pollers) == 1 {
		scorePoller = pollers[0]
	} else {
		scorePoller = tracker.NewMultiScorePoller(pollers...)
	}
	tr := tracker.New(wsMgr, scorePoller, log)
	tr.SetPriceCleaner(multi)
	tr.SetMarketRegistrar(multi)

	// Scanner
	sc := scanner.New(restClient, db, log)

	// Scheduler
	sched := scheduler.New(db, tr, log)

	// errgroup for top-level goroutines
	g, ctx := errgroup.WithContext(ctx)

	// Backtest engine for dashboard strategy API
	btEngine, err := backtest.NewEngine(log)
	if err != nil {
		log.Error("backtest engine init failed", "err", err)
		os.Exit(1)
	}
	defer btEngine.Close()

	// Backtest result cache — TTL from env config (default 30 min)
	btCacheTTL := time.Duration(config.Cfg.BacktestCacheTTLMin) * time.Minute
	btCache := backtest.NewCache(btCacheTTL)

	// pprof + runtime metrics + strategy API server
	if config.Cfg.MetricsAddr != "" {
		apiSrv := dashboardapi.NewServer(dashboardapi.Deps{
			Tracker: tr,
			Engine:  btEngine,
			Cache:   btCache,
			DB:      db,
			Log:     log,
		})
		metricsSrv := &http.Server{
			Addr:         config.Cfg.MetricsAddr,
			Handler:      apiSrv.Handler(),
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

	// 1. Tick writer (single DB writer)
	g.Go(func() error {
		return tickWriter.Run(ctx)
	})

	// 2. WebSocket manager (auto-reconnect, dispatch)
	g.Go(func() error {
		return wsMgr.Run(ctx)
	})

	// 3. Scanner loop (daily scan for new matches)
	g.Go(func() error {
		return sc.RunLoop(ctx, time.Duration(config.Cfg.ScanIntervalHours)*time.Hour)
	})

	// 4. Scheduler loop (poll DB, schedule tracking at occurrence_datetime - lead)
	g.Go(func() error {
		return sched.Run(ctx)
	})

	// 4b. Reconciler loop (fill settlement gaps via REST for unresolved markets)
	recon := reconciler.New(restClient, db, log)
	g.Go(func() error {
		return recon.Run(ctx)
	})

	// 4c. Order backfill loop (refresh stale real order status from REST)
	obf := orderbackfill.New(restClient, db, log)
	g.Go(func() error {
		return obf.Run(ctx)
	})

	// 4d. Schedule checker loop (refresh stale occurrence_ts from REST)
	schedChk := schedulechecker.New(restClient, db, multi, log)
	g.Go(func() error {
		return schedChk.Run(ctx)
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

	// 6. Strategy timer goroutine — drives periodic OnTick calls (close_timer etc)
	g.Go(func() error {
		return multi.RunTimer(ctx)
	})

	// 7. Backtest cache pre-warm — runs all strategies every TTL to keep cache fresh
	g.Go(func() error {
		ticker := time.NewTicker(btCacheTTL)
		defer ticker.Stop()
		// pre-warm immediately at startup
		btEngine.PrewarmCache(btCache, log)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				btEngine.PrewarmCache(btCache, log)
			}
		}
	})

	// 8. Price bands cron — compute missing days hourly, persist to DB
	g.Go(func() error {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		pricebands.ComputeMissingDays(btEngine, db, log) // initial run at startup
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				pricebands.ComputeMissingDays(btEngine, db, log)
			}
		}
	})

	log.Info("ghost trader running", "scan_interval", time.Duration(config.Cfg.ScanIntervalHours)*time.Hour, "lead_minutes", config.Cfg.TrackLeadMinutes)

	// Wait for shutdown signal or critical failure
	err = g.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("shutdown error", "err", err)
	}

	// Orderly teardown
	tr.StopAll()
	log.Info("clean shutdown complete")
}
