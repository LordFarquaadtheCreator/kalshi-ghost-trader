// Command backfill recomputes is_set_point and is_match_point flags for
// existing rows in the points table. Run once after deploying the Go-side
// point classification logic.
//
// Usage:
//
//	go run ./cmd/backfill -db kalshi_tennis.db
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func main() {
	dbPath := flag.String("db", "kalshi_tennis.db", "path to SQLite DB")
	dryRun := flag.Bool("dry-run", false, "print what would change without writing")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := context.Background()

	db, err := store.New(ctx, *dbPath, log)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Error("schema migration failed", "err", err)
		os.Exit(1)
	}

	points, err := db.GetAllPoints(ctx)
	if err != nil {
		log.Error("load points", "err", err)
		os.Exit(1)
	}

	log.Info("loaded points", "count", len(points))

	// Group by match to track set counts
	type matchKey struct {
		ticker string
	}
	matchSets := make(map[string]int) // ticker → sets won by home
	matchSetsAway := make(map[string]int)

	updated := 0
	skipped := 0

	for _, p := range points {
		// Track set count from HomeSetGames/AwaySetGames
		setsHome := p.HomeSetGames
		setsAway := p.AwaySetGames

		pc := algorithms.ClassifyPoint(algorithms.PointContext{
			SetsHome:   setsHome,
			SetsAway:   setsAway,
			HomeGames:  p.HomeGames,
			AwayGames:  p.AwayGames,
			HomePoints: p.HomePoints,
			AwayPoints: p.AwayPoints,
			Server:     p.Server,
			IsTiebreak: p.IsTiebreak,
		})

		changed := false
		if pc.IsSetPoint != p.IsSetPoint {
			changed = true
		}
		if pc.IsMatchPoint != p.IsMatchPoint {
			changed = true
		}

		if !changed {
			skipped++
			continue
		}

		if *dryRun {
			fmt.Printf("DRY: %s set=%d game=%d pt=%d: set_point %v→%v, match_point %v→%v\n",
				p.MatchTicker, p.SetNumber, p.GameNumber, p.PointNumber,
				p.IsSetPoint, pc.IsSetPoint, p.IsMatchPoint, pc.IsMatchPoint)
			updated++
			continue
		}

		if err := db.UpdatePointFlags(ctx, p.MatchTicker, p.SetNumber, p.GameNumber, p.PointNumber,
			pc.IsSetPoint, pc.IsMatchPoint); err != nil {
			log.Error("update flags", "err", err, "match", p.MatchTicker,
				"set", p.SetNumber, "game", p.GameNumber, "pt", p.PointNumber)
			continue
		}
		updated++
	}

	log.Info("backfill complete",
		"total", len(points),
		"updated", updated,
		"skipped", skipped,
		"dry_run", *dryRun)

	_ = matchSets
	_ = matchSetsAway
	_ = strconv.Itoa
}
