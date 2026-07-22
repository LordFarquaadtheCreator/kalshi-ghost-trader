// Command backfill-orders is a one-shot CLI that catches up stale real orders
// and unresolved markets. Equivalent to running orderbackfill + reconciler once
// across the entire backlog.
//
// Usage:
//
//	go run ./cmd/backfill-orders              # backfill + resolve
//	go run ./cmd/backfill-orders -orders      # only backfill order status
//	go run ./cmd/backfill-orders -markets     # only resolve markets
//	go run ./cmd/backfill-orders -dry-run     # show what would change, no writes
//
// Connects via the DSN in app.dev.yaml / app.yaml. Signs requests with the
// Kalshi RSA key from kalshi_private_key_path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/farquaad/kalshi-ghost-trader/internal/appconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiAuth"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// closeGraceMS matches reconciler default — markets past close_ts + 30min
// are considered overdue for settlement.
const closeGraceMS = 30 * 60 * 1000

// matchDurationBufferMS matches reconciler default — markets past
// occurrence_ts + 6h are considered overdue (tennis close_ts can be weeks out).
const matchDurationBufferMS = 6 * 60 * 60 * 1000

func main() {
	onlyOrders := flag.Bool("orders", false, "only backfill order status, skip market resolution")
	onlyMarkets := flag.Bool("markets", false, "only resolve markets, skip order status backfill")
	dryRun := flag.Bool("dry-run", false, "show what would change without writing")
	logLevel := flag.String("log-level", "INFO", "log level (DEBUG, INFO, WARN, ERROR)")
	flag.Parse()

	if *onlyOrders && *onlyMarkets {
		fmt.Fprintln(os.Stderr, "-orders and -markets are mutually exclusive")
		os.Exit(2)
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintf(os.Stderr, "bad log-level: %v\n", err)
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	ctx := context.Background()

	appCfg, err := appconfig.Load()
	if err != nil {
		log.Error("appconfig load", "err", err)
		os.Exit(1)
	}
	db, err := store.New(ctx, appCfg.DBDSN, log)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// config.Load sets config.Cfg which kalshiclient.NewClient reads for
	// RESTBaseURL / rate limit / timeout. Required before constructing client.
	if _, err := config.Load(db); err != nil {
		log.Error("config load", "err", err)
		os.Exit(1)
	}

	signer, err := kalshiAuth.NewSignerFromFile()
	if err != nil {
		log.Error("signer init", "err", err)
		os.Exit(1)
	}
	client := kalshiclient.NewClient(signer, log)

	doOrders := !*onlyMarkets
	doMarkets := !*onlyOrders

	if doOrders {
		if err := backfillOrders(ctx, client, db, log, *dryRun); err != nil {
			log.Error("orders backfill failed", "err", err)
			os.Exit(1)
		}
	}
	if doMarkets {
		if err := resolveMarkets(ctx, client, db, log, *dryRun); err != nil {
			log.Error("markets resolve failed", "err", err)
			os.Exit(1)
		}
	}
}

// backfillOrders fetches every non-terminal real order from Kalshi REST and
// updates status + fill_count. Mirrors orderbackfill.backfill but runs once
// across the full backlog.
func backfillOrders(ctx context.Context, client *kalshiclient.Client, db *store.DB, log *slog.Logger, dryRun bool) error {
	orders, err := db.GetUnresolvedRealOrders(ctx)
	if err != nil {
		return fmt.Errorf("get unresolved orders: %w", err)
	}
	if len(orders) == 0 {
		log.Info("no unresolved real orders")
		return nil
	}
	log.Info("backfilling real orders", "count", len(orders), "dry_run", dryRun)

	updated := 0
	for _, o := range orders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		od, err := client.GetOrder(ctx, o.KalshiOrderID)
		if err != nil {
			log.Warn("fetch order failed", "order_id", o.KalshiOrderID, "err", err)
			continue
		}

		var internalStatus string
		switch od.Status {
		case "resting":
			internalStatus = "submitted"
		case "canceled":
			internalStatus = "canceled"
		case "executed":
			internalStatus = "filled"
		default:
			internalStatus = od.Status
		}
		if internalStatus == o.OrderStatus {
			continue
		}
		fillCount := parseFP(od.FillCountFP)

		log.Info("order status change",
			"order_id", o.KalshiOrderID,
			"market", o.MarketTicker,
			"old", o.OrderStatus,
			"new", internalStatus,
			"fill_count", fillCount,
			"dry_run", dryRun)

		if dryRun {
			updated++
			continue
		}
		if err := db.UpdateRealOrderStatus(ctx, o.ID, fillCount, internalStatus); err != nil {
			log.Error("update order failed", "order_id", o.KalshiOrderID, "err", err)
			continue
		}
		updated++
	}
	log.Info("orders backfill complete", "checked", len(orders), "updated", updated)
	return nil
}

// resolveMarkets fetches every market with orders but no result (or past close
// + grace) from Kalshi REST, updates the market row, and resolves orders for
// any market with a result. Mirrors reconciler.reconcile but runs once across
// the full backlog and resolves per-market (not gated on event finalization).
func resolveMarkets(ctx context.Context, client *kalshiclient.Client, db *store.DB, log *slog.Logger, dryRun bool) error {
	markets, err := db.GetUnresolvedMarkets(ctx, closeGraceMS, matchDurationBufferMS)
	if err != nil {
		return fmt.Errorf("get unresolved markets: %w", err)
	}
	if len(markets) == 0 {
		log.Info("no unresolved markets")
		return nil
	}
	log.Info("resolving markets", "count", len(markets), "dry_run", dryRun)

	finalized := make(map[string]bool)
	updated, resolved := 0, 0
	for _, m := range markets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mkt, err := client.GetMarket(ctx, m.MarketTicker)
		if err != nil {
			log.Warn("fetch market failed", "market", m.MarketTicker, "err", err)
			continue
		}
		// Skip if REST has no result AND status unchanged
		if mkt.Result == "" && mkt.Status == m.Status {
			continue
		}

		log.Info("market update",
			"market", m.MarketTicker,
			"old_status", m.Status,
			"new_status", mkt.Status,
			"result", mkt.Result,
			"dry_run", dryRun)

		if dryRun {
			updated++
			if mkt.Result != "" {
				resolved++
			}
			continue
		}

		if _, err := db.UpsertMarketCheckNew(ctx, store.Market{
			MarketTicker:     mkt.Ticker,
			EventTicker:      mkt.EventTicker,
			SeriesTicker:     m.SeriesTicker,
			PlayerName:       mkt.YesSubTitle,
			TennisCompetitor: kalshiclient.ParseTennisCompetitor(mkt.CustomStrike),
			Status:           mkt.Status,
			OccurrenceTS:     kalshiclient.ParseISOTime(mkt.OccurrenceDatetime, log),
			OpenTS:           kalshiclient.ParseISOTime(mkt.OpenTime, log),
			CloseTS:          kalshiclient.ParseISOTime(mkt.CloseTime, log),
			Result:           mkt.Result,
			SettlementTS:     kalshiclient.ParseISOTime(mkt.SettlementTS, log),
			SettlementValue:  mkt.SettlementValueDollars,
		}); err != nil {
			log.Error("update market failed", "market", m.MarketTicker, "err", err)
			continue
		}
		updated++

		// Resolve orders per-market — independent of event finalization.
		// Tennis events have 2 markets; gating on finalized[event] skips the
		// second market.
		if mkt.Result != "" {
			if err := db.ResolveRealOrders(ctx, m.MarketTicker, mkt.Result); err != nil {
				log.Warn("resolve real orders failed", "market", m.MarketTicker, "err", err)
			} else {
				log.Info("resolved real orders", "market", m.MarketTicker, "result", mkt.Result)
			}
			if err := db.ResolveSimulatedOrders(ctx, m.MarketTicker, mkt.Result); err != nil {
				log.Warn("resolve simulated orders failed", "market", m.MarketTicker, "err", err)
			} else {
				log.Info("resolved simulated orders", "market", m.MarketTicker, "result", mkt.Result)
			}
			resolved++
		}

		// Finalize event once both markets finalized (coverage classification,
		// payload pruning). Same dedup as reconciler.
		if mkt.Status == "finalized" && m.EventTicker != "" && !finalized[m.EventTicker] {
			finalized[m.EventTicker] = true
			if err := db.FinalizeEventIfNeeded(ctx, m.EventTicker); err != nil {
				log.Warn("finalize event failed", "event", m.EventTicker, "err", err)
			}
		}
	}
	log.Info("markets resolve complete", "checked", len(markets), "updated", updated, "resolved", resolved)
	return nil
}

// parseFP parses a Kalshi fixed-point count string (e.g. "5.00") to float64.
func parseFP(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
