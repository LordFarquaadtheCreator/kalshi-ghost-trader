// devserver — minimal v2 API server for local development.
// Opens the DB, runs migrations, serves the HTTP API on :6060.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/httpapi"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "postgres://kalshi:kalshi@localhost:5432/kalshi_tennis?sslmode=disable"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Error("db open failed", "err", err)
		os.Exit(1)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(5)

	log.Info("devserver: db connected", "dsn", dsn)

	srv := httpapi.NewServer(db, log)
	// devserver has no tracker — /api/tracked returns empty.
	addr := ":6060"
	log.Info("devserver: starting", "addr", addr)

	if err := srv.Serve(ctx, addr); err != nil {
		log.Error("devserver: serve failed", "err", err)
		os.Exit(1)
	}
}
