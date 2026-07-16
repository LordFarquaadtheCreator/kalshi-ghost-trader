// Command ghost-trader is the main entrypoint for the Kalshi Ghost Trader service.
//
// It loads configuration from config.yaml, initializes an RSA signer for Kalshi
// API authentication, opens a SQLite database with WAL mode, and launches all
// core goroutines via errgroup:
//
//   - TickWriter — single SQLite writer for batched tick/orderbook/lifecycle/points inserts
//   - WebSocket Manager — auto-reconnecting connection to Kalshi's real-time feed
//   - Scanner — periodic REST scan for new tennis events and markets
//   - Scheduler — starts per-market WS tracking at occurrence_datetime minus lead time
//   - FlashScore Scraper — optional point-by-point tennis data polling
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
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/flashscore"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiauth"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/scanner"
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

	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}
	log.Info("config loaded", "env", cfg.Environment, "db", cfg.DBPath, "series_count", len(cfg.SeriesTickers))

	// Load signer
	signer, err := kalshiauth.NewSignerFromFile(cfg.APIKeyID, cfg.PrivateKeyPath)
	if err != nil {
		log.Error("signer init failed", "err", err)
		os.Exit(1)
	}

	// Root context cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Open SQLite store
	db, err := store.New(ctx, cfg.DBPath, log)
	if err != nil {
		log.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Tick writer (single writer goroutine for batch inserts)
	tickWriter := db.NewTickWriter(cfg.BatchSize, cfg.FlushTimeoutMS, log)

	// REST client
	restClient := kalshiclient.NewClient(cfg.RESTBaseURL, signer,
		time.Duration(cfg.HTTPTimeoutSecs)*time.Second, cfg.RateLimitRPS, log)

	// Shared emitter — all strategies tag orders and forward here
	sharedEmitter := algorithms.NewTickWriterEmitter(tickWriter)

	// matchPoint is the primary strategy and also serves as PriceLookup for CloseTimer
	matchPoint := algorithms.NewMatchPointStrategy(sharedEmitter, log)

	// Multi-strategy runtime: all point-based strategies run simultaneously.
	// Each strategy's orders are tagged with its name in the orders table.
	multi := algorithms.NewMultiStrategyFromFactories(sharedEmitter, log, map[string]algorithms.StrategyFactoryFn{
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
	})
	log.Info("multi-strategy runtime initialized", "strategies", multi.String())

	// Close timer strategy (buy favorite N min before close)
	var closeTimer *sigpkg.CloseTimer
	if cfg.CloseTimerEnabled {
		closeTimer = sigpkg.NewCloseTimer(db, matchPoint, tickWriter,
			cfg.CloseTimerLeadMin, cfg.CloseTimerMinPrice, cfg.CloseTimerSize, log)
		log.Info("close timer strategy enabled",
			"lead_min", cfg.CloseTimerLeadMin,
			"min_price", cfg.CloseTimerMinPrice,
			"size", cfg.CloseTimerSize)
	}

	// WebSocket manager
	wsMgr := wsclient.NewManager(cfg.WSURL, signer, tickWriter, cfg.SeriesTickers,
		time.Duration(cfg.WSMinBackoffSecs)*time.Second,
		time.Duration(cfg.WSMaxBackoffSecs)*time.Second,
		log)
	wsMgr.SetPriceUpdater(multi)

	// FlashScore scraper (optional — created before tracker so tracker can drive polling)
	var fsScraper *flashscore.Scraper
	if cfg.FlashScoreEnabled {
		fsScan := time.Duration(cfg.FlashScoreScanInterval) * time.Second
		fsPoll := time.Duration(cfg.FlashScorePollInterval) * time.Second
		fsScraper = flashscore.New(db, tickWriter, multi, fsScan, fsPoll,
			cfg.FlashScoreLookaheadDays, log)
		log.Info("flashscore scraper enabled",
			"scan_interval", fsScan, "poll_interval", fsPoll)
	}

	// API-Tennis scraper (optional — WebSocket real-time push, no polling delay)
	var atScraper *apitennis.Scraper
	if cfg.APITennisEnabled {
		if cfg.APITennisAPIKey == "" {
			log.Error("apitennis_enabled but apitennis_api_key is empty")
			os.Exit(1)
		}
		atScraper = apitennis.New(db, tickWriter, multi, cfg.APITennisAPIKey,
			cfg.APITennisTimezone, log)
		log.Info("apitennis scraper enabled", "timezone", cfg.APITennisTimezone)
	}

	// Tracker (market subscription lifecycle)
	// Score poller coupling: tracker drives polling on subscribe/unsubscribe
	var scorePoller tracker.ScorePoller
	if atScraper != nil {
		scorePoller = atScraper
	} else if fsScraper != nil {
		scorePoller = fsScraper
	}
	tr := tracker.New(wsMgr, scorePoller, log)
	tr.SetPriceCleaner(multi)

	// Scanner
	sc := scanner.New(restClient, db, cfg.SeriesTickers, log)

	// Scheduler
	sched := scheduler.New(db, tr, cfg.TrackLeadMinutes, log)

	// errgroup for top-level goroutines
	g, ctx := errgroup.WithContext(ctx)

	// pprof + runtime metrics server
	if cfg.MetricsPort > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/metrics", metricsHandler)
		mux.Handle("/debug/pprof/", http.DefaultServeMux)
		metricsSrv := &http.Server{
			Addr:         fmt.Sprintf("127.0.0.1:%d", cfg.MetricsPort),
			Handler:      corsMiddleware(mux),
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
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

	// 5. FlashScore scraper goroutine (optional — polls tennis point-by-point data)
	if fsScraper != nil {
		g.Go(func() error {
			return fsScraper.Run(ctx)
		})
	}

	// 5b. API-Tennis scraper goroutine (optional — WS real-time push)
	if atScraper != nil {
		g.Go(func() error {
			return atScraper.Run(ctx)
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
