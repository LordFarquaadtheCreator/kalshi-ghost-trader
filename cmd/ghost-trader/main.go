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

	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/flashscore"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiauth"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/scanner"
	"github.com/farquaad/kalshi-ghost-trader/internal/scheduler"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
	wsclient "github.com/farquaad/kalshi-ghost-trader/internal/ws"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

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

	// WebSocket manager
	wsMgr := wsclient.NewManager(cfg.WSURL, signer, tickWriter, cfg.SeriesTickers,
		time.Duration(cfg.WSMinBackoffSecs)*time.Second,
		time.Duration(cfg.WSMaxBackoffSecs)*time.Second,
		log)

	// Tracker (market subscription lifecycle)
	tr := tracker.New(wsMgr, log)

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

	// 5. FlashScore scraper (optional — polls tennis point-by-point data)
	if cfg.FlashScoreEnabled {
		fsScan := time.Duration(cfg.FlashScoreScanInterval) * time.Second
		fsPoll := time.Duration(cfg.FlashScorePollInterval) * time.Second
		fsScraper := flashscore.New(db, tickWriter, fsScan, fsPoll,
			cfg.FlashScoreLookaheadDays, log)
		g.Go(func() error {
			return fsScraper.Run(ctx)
		})
		log.Info("flashscore scraper enabled",
			"scan_interval", fsScan, "poll_interval", fsPoll)
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
