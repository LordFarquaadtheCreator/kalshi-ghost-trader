// Package main is the v2 ghost-trader entrypoint. Wiring only.
// No package may reach for a global; everything is passed in.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	g, ctx := errgroup.WithContext(ctx)

	// 1. Config
	// cfg, err := config.Load(ctx, db, "app.yaml")
	// if err != nil { log.Error("config load failed", "err", err); os.Exit(1) }

	// 2. DB + migrations
	// db, err := gorm.Open(...)
	// run migrations

	// 3. Writer (ingest)
	// copyWriter := postgres.NewCopyWriter(db)
	// writer := ingest.NewWriter(copyWriter, 4096, 5*time.Second, log)
	// g.Go(func() error { return writer.Run(ctx) })

	// 4. Order worker
	// gates := orders.NewGateCache(...)
	// ledgerRepo := postgres.NewLedgerRepo(db)
	// worker := orders.NewWorker(gates, ledgerRepo, exchange, orderRepo, log, ...)
	// g.Go(func() error { return worker.Run(ctx) })

	// 5. Tracker
	// tracker := tracker.NewTracker(stream, scoreFeed, log)
	// tracker.SetWorker(worker)
	// g.Go(func() error { return tracker.Run(ctx) })

	// 6. HTTP API
	// g.Go(func() error { return httpapi.Serve(ctx, addr, db, log) })

	_ = ctx
	_ = g

	log.Info("ghost-trader v2 starting (stub — full wiring pending DB/config integration)")
	<-ctx.Done()
	log.Info("ghost-trader v2 shutting down")
}
