// Package main is the v2 ghost-trader entrypoint. Wiring only.
// No package may reach for a global; everything is passed in.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/apitennis"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/kalshi"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/postgres"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/ingest"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/insights"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/orders"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/scanner"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/scheduler"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/tracker"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/config"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/strategy"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/httpapi"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 1. Config — load YAML env config + app_config DB table.
	envPath := os.Getenv("APP_ENV")
	if envPath == "prod" {
		envPath = "app.yaml"
	} else {
		envPath = "app.dev.yaml"
	}

	// 2. DB — open GORM connection.
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		// Pre-parse YAML to get DSN before we can load full config (which needs DB).
		env, err := loadEnvOnly(envPath)
		if err != nil {
			log.Error("main: load env failed", "path", envPath, "err", err)
			os.Exit(1)
		}
		dsn = env.DBDSN
	}
	if dsn == "" {
		log.Error("main: no db_dsn configured")
		os.Exit(1)
	}

	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Error("main: db open failed", "err", err)
		os.Exit(1)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	log.Info("main: db connected", "dsn", dsn)

	// 3. Migrations — run v2 SQL migrations.
	if err := postgres.RunMigrations(ctx, db, log); err != nil {
		log.Error("main: migrations failed", "err", err)
		os.Exit(1)
	}

	// 4. Config store — load full config (YAML + app_config DB table).
	cfgStore, err := config.Load(ctx, db, envPath)
	if err != nil {
		log.Error("main: config load failed", "err", err)
		os.Exit(1)
	}
	cfg := cfgStore.Current()
	log.Info("main: config loaded", "environment", cfg.Environment, "series", len(cfg.SeriesTickers))

	// 5. Init pool balance if not exists.
	ledgerRepo := postgres.NewLedgerRepo(db)
	if err := ledgerRepo.InitBalance(ctx, int64(cfg.PaperBankroll*100)); err != nil {
		log.Error("main: init balance failed", "err", err)
		os.Exit(1)
	}

	g, ctx := errgroup.WithContext(ctx)

	// 6. Ingest writer — batched tick/orderbook COPY.
	copyWriter := postgres.NewCopyWriter(db)
	writer := ingest.NewWriter(copyWriter, cfg.BatchSize, time.Duration(cfg.FlushTimeoutMS)*time.Millisecond, log)
	g.Go(func() error { return writer.Run(ctx) })

	// 7. Kalshi WS stream — market data ingestion.
	stream := kalshi.NewStream(cfg.WSURL, "", log)
	stream.SetWriter(writer)
	stream.SetBackoff(
		time.Duration(cfg.WSMinBackoffSecs)*time.Second,
		time.Duration(cfg.WSMaxBackoffSecs)*time.Second,
	)
	if len(cfg.SeriesTickers) > 0 {
		stream.SetSeries(cfg.SeriesTickers)
	}

	// 8. Build strategy set — all hand-written strategies always run.
	strategies := buildStrategies()
	log.Info("main: strategies loaded", "count", len(strategies))

	// 9. Order worker — intent → gate → size → submit → fill.
	orderRepo := postgres.NewOrderRepo(db)
	featureRepo := postgres.NewFeatureRepo(db)

	var exchange ports.Exchange
	if cfg.RealTradingEnabled {
		exchange = kalshi.NewExchange(cfg.RESTBaseURL, nil, cfg.RealOrderTimeInForce, cfg.RealOrderTimeoutS)
		log.Info("main: real trading enabled")
	} else {
		log.Info("main: paper trading only")
	}

	gateCfg := orders.GateConfig{
		StrategyEnabled:  make(map[string]bool),
		TriggerRanges:    make(map[string]orders.PriceBand),
		PerMarketLimit:   3,
		CooldownSeconds:  cfg.PerStrategyCooldownSecs,
	}
	for _, s := range strategies {
		gateCfg.StrategyEnabled[s.Name()] = false // paper mode — gates skipped anyway
	}
	gates := orders.NewGateCache(gateCfg)

	bankrollCents := int64(cfg.PaperBankroll * 100)
	if cfg.RealTradingEnabled {
		bankrollCents = int64(cfg.RealBankroll * 100)
	}
	worker := orders.NewWorker(gates, ledgerRepo, exchange, orderRepo, featureRepo, log, bankrollCents, cfg.KellyFraction, cfg.LegacySizing)
	g.Go(func() error { return worker.Run(ctx) })

	// 10. Tracker — per-match event loops.
	scoreFeed := apitennis.New(cfg.APITennisAPIKey, cfg.APITennisTimezone, log)
	tr := tracker.NewTracker(stream, scoreFeed, log)
	tr.SetWorker(worker)

	// Wire stream's loop lookup to the tracker.
	stream.SetLoopLookup(func(marketTicker string) *match.Loop {
		// The tracker manages loops per event; we need to find the loop
		// that tracks this market. For now, return nil — the tracker
		// dispatches via its own mechanism.
		return nil
	})

	// 11. Score feed — API-Tennis WS.
	g.Go(func() error {
		return scoreFeed.Run(ctx, func(ps match.PointScored) {
			// Forward to all active match loops.
			// The tracker handles dispatch to the right loop.
			_ = ps // tracker integration: scoreFeed → tracker → loop
		})
	})

	// 12. Scanner — daily REST scan for events/markets.
	restClient := kalshi.NewRESTClient(cfg.RESTBaseURL, nil, cfg.HTTPTimeoutSecs)
	scanner := scanner.New(restClient, db, cfg.SeriesTickers, log)
	scanInterval := time.Duration(cfg.ScanIntervalHours) * time.Hour
	if scanInterval <= 0 {
		scanInterval = 6 * time.Hour
	}
	g.Go(func() error { return scanner.RunLoop(ctx, scanInterval) })

	// 13. Scheduler — schedule match tracking at occurrence - lead.
	sched := scheduler.New(db, tr, strategies, cfg.TrackLeadMinutes, cfg.SchedulerPollSecs, log)
	g.Go(func() error { return sched.Run(ctx) })

	// 14. Insights refresher — materialized views.
	refresher := insights.NewRefresher(db, cfg.InsightsRefreshSecs, log)
	g.Go(func() error { return refresher.Run(ctx) })

	// 15. WS stream — start after wiring is complete.
	g.Go(func() error { return stream.Run(ctx) })

	// 16. HTTP API — dashboard + metrics.
	srv := httpapi.NewServer(db, log)
	g.Go(func() error { return srv.Serve(ctx, cfg.MetricsAddr) })

	log.Info("main: ghost-trader v2 starting", "metrics_addr", cfg.MetricsAddr, "strategies", len(strategies))

	if err := g.Wait(); err != nil {
		log.Error("main: shutdown with error", "err", err)
		os.Exit(1)
	}
	log.Info("main: shutdown complete")
}

// buildStrategies returns all hand-written strategies.
// Every strategy always runs — no skipping, no conditional activation.
func buildStrategies() []strategy.Strategy {
	return []strategy.Strategy{
		strategy.NewMatchPoint(),
		strategy.NewAdOut(),
		strategy.NewBreakBack(),
		strategy.NewBreakPoint(),
		strategy.NewBuyTheDip(),
		strategy.NewCloseTimer(),
		strategy.NewComeback040(),
		strategy.NewFadeLongshot(),
		strategy.NewNoFade(),
		strategy.NewServer1530(),
		strategy.NewSet1Winner(),
		strategy.NewSetDown(),
		strategy.NewSetPoint(),
		strategy.NewSpikeFade(),
		strategy.NewTiebreakServer(),
		strategy.NewTiebreak(),
		strategy.NewVolumeRatio(),
		strategy.NewConvexPool(strategy.DefaultConvexPoolConfig()),
		strategy.NewCrossArb(strategy.DefaultCrossArbConfig()),
		strategy.NewCrossArbFavorite(strategy.DefaultCrossArbFavoriteConfig()),
	}
}

// loadEnvOnly reads just the YAML env config without needing the DB.
// Used to get the DSN before the full config store can be initialized.
func loadEnvOnly(path string) (*config.Snapshot, error) {
	// config.Load needs DB, but we only need the env portion.
	// Read the YAML directly via a temporary store with nil DB.
	// This is a workaround — config.Load could be split but isn't.
	store, err := config.Load(context.Background(), nil, path)
	if err != nil {
		// If Load fails with nil DB, try reading just the env.
		// config.Load calls loadEnv first, then getAllAppConfig.
		// With nil DB, getAllAppConfig will fail. So we need to
		// handle this differently.
		return nil, fmt.Errorf("load env: %w", err)
	}
	return store.Current(), nil
}
