// Command featuredump replays historical events from ticks_v2 through the
// production feature extractor, emitting one row per decision point to Parquet.
// This is the single code path for training data — no second feature
// implementation in Python.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/features"
	"github.com/parquet-go/parquet-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// FeatureRow is one decision point — the exact feature vector the strategy
// saw at a point in time, plus the metadata needed to join labels later.
type FeatureRow struct {
	EventTicker   string  `parquet:"event_ticker"`
	MarketTicker  string  `parquet:"market_ticker"`
	TS            int64   `parquet:"ts"`
	FeatureHash   string  `parquet:"feature_hash"`
	PriceCents    int     `parquet:"price_cents"`
	BestBidCents  int     `parquet:"best_bid_cents"`
	BestAskCents  int     `parquet:"best_ask_cents"`
	BestBidSize   int     `parquet:"best_bid_size"`
	BestAskSize   int     `parquet:"best_ask_size"`
	// Features are stored as a JSON string for flexibility.
	Features      string  `parquet:"features"`
	// Label columns — joined later by the training pipeline.
	SettledOutcome *int   `parquet:"settled_outcome,optional"`
	RealizedPnl    *int64 `parquet:"realized_pnl,optional"`
}

func main() {
	var (
		dsn     = flag.String("dsn", "", "PostgreSQL DSN")
		from    = flag.String("from", "", "Start date (YYYY-MM-DD)")
		to      = flag.String("to", "", "End date (YYYY-MM-DD)")
		out     = flag.String("out", "features.parquet", "Output Parquet file")
	)
	flag.Parse()

	if *dsn == "" {
		*dsn = os.Getenv("DB_DSN")
	}
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "error: --dsn or DB_DSN required")
		os.Exit(1)
	}
	if *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "error: --from and --to required (YYYY-MM-DD)")
		os.Exit(1)
	}

	log := slog.Default()

	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect db: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	fromTs, err := time.Parse("2006-01-02", *from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse --from: %v\n", err)
		os.Exit(1)
	}
	toTs, err := time.Parse("2006-01-02", *to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse --to: %v\n", err)
		os.Exit(1)
	}

	fromMs := fromTs.UnixMilli()
	toMs := toTs.Add(24 * time.Hour).UnixMilli()

	rows, err := dumpFeatures(ctx, db, log, fromMs, toMs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: dump: %v\n", err)
		os.Exit(1)
	}

	if err := writeParquet(*out, rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: write parquet: %v\n", err)
		os.Exit(1)
	}

	log.Info("featuredump complete", "rows", len(rows), "out", *out)
}

// dumpFeatures replays ticks from the DB through the feature extractor.
func dumpFeatures(ctx context.Context, db *gorm.DB, log *slog.Logger, fromMs, toMs int64) ([]FeatureRow, error) {
	if log == nil {
		log = slog.Default()
	}
	extractor := features.NewDefaultExtractor()
	featureHash := extractor.Hash()
	names := extractor.Names()

	type tickRow struct {
		EventTicker  string
		MarketTicker string
		TS           int64
		PriceCents   int
		BestBidCents *int
		BestAskCents *int
		BestBidSize  *int
		BestAskSize  *int
	}

	var ticks []tickRow
	err := db.WithContext(ctx).Raw(`
		SELECT t.market_ticker, t.ts, t.price_cents,
			t.best_bid_cents, t.best_ask_cents, t.best_bid_size, t.best_ask_size,
			m.event_ticker
		FROM ticks_v2 t
		JOIN markets m ON t.market_ticker = m.market_ticker
		WHERE t.ts >= ? AND t.ts < ?
		ORDER BY t.ts
	`, fromMs, toMs).Scan(&ticks).Error
	if err != nil {
		return nil, fmt.Errorf("query ticks: %w", err)
	}

	log.Info("featuredump: loaded ticks", "count", len(ticks))

	rows := make([]FeatureRow, 0, len(ticks))
	for _, t := range ticks {
		v := features.View{
			PriceCents: t.PriceCents,
		}
		if t.BestBidCents != nil {
			v.BestBidCents = *t.BestBidCents
		}
		if t.BestAskCents != nil {
			v.BestAskCents = *t.BestAskCents
		}
		if t.BestBidSize != nil {
			v.BestBidSize = *t.BestBidSize
		}
		if t.BestAskSize != nil {
			v.BestAskSize = *t.BestAskSize
		}

		vec := extractor.Extract(v, nil)

		// Serialize features as JSON.
		featMap := make(map[string]float64, len(names))
		for i, name := range names {
			featMap[name] = vec[i]
		}
		featuresJSON := featuresToJSON(featMap)

		row := FeatureRow{
			EventTicker:  t.EventTicker,
			MarketTicker: t.MarketTicker,
			TS:           t.TS,
			FeatureHash:  featureHash,
			PriceCents:   t.PriceCents,
			BestBidCents: v.BestBidCents,
			BestAskCents: v.BestAskCents,
			BestBidSize:  v.BestBidSize,
			BestAskSize:  v.BestAskSize,
			Features:     featuresJSON,
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// writeParquet writes feature rows to a Parquet file.
func writeParquet(path string, rows []FeatureRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	pw := parquet.NewGenericWriter[FeatureRow](f)
	_, err = pw.Write(rows)
	if err != nil {
		return fmt.Errorf("write parquet: %w", err)
	}
	if err := pw.Close(); err != nil {
		return fmt.Errorf("close parquet: %w", err)
	}
	return nil
}

// featuresToJSON serializes a feature map to a compact JSON string
// with keys in sorted order for deterministic output.
func featuresToJSON(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b []byte
	b = append(b, '{')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, k...)
		b = append(b, '"', ':')
		b = append(b, fmt.Sprintf("%g", m[k])...)
	}
	b = append(b, '}')
	return string(b)
}
