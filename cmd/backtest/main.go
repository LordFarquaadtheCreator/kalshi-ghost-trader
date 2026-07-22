// Command backtest replays historical point + tick data from the PostgreSQL DB
// through a trading strategy and reports P&L.
//
// Usage:
//
//	go run ./cmd/backtest -strategy <name> [flags]
//
// Connects via the DSN in app.dev.yaml / app.yaml.
// Use -strategy all to run every registered strategy.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/farquaad/kalshi-ghost-trader/internal/appconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
)

func main() {
	strategyName := flag.String("strategy", "all", "strategy to backtest (use 'all' for all strategies)")
	minPrice := flag.Float64("min-price", 0.0, "skip signals below this market price (0=disabled)")
	maxPrice := flag.Float64("max-price", 0.0, "skip signals above this market price (0=disabled)")
	debugMode := flag.Bool("debug", false, "enable debug logging to see strategy filter reasons")
	flag.Parse()

	logLevel := slog.LevelWarn
	if *debugMode {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	appCfg, err := appconfig.Load()
	if err != nil {
		log.Error("appconfig load", "err", err)
		os.Exit(1)
	}

	engine, err := backtest.NewEngineFromDSN(log, appCfg.DBDSN)
	if err != nil {
		log.Error("backtest engine init", "err", err)
		os.Exit(1)
	}
	defer engine.Close()

	available := engine.AvailableStrategies()

	var selected []string
	if *strategyName == "all" {
		selected = available
	} else {
		found := false
		for _, name := range available {
			if name == *strategyName {
				found = true
				break
			}
		}
		if !found {
			list := append([]string{"all"}, available...)
			fmt.Fprintf(os.Stderr, "unknown strategy %q (available: %s)\n", *strategyName, strings.Join(list, ", "))
			os.Exit(1)
		}
		selected = []string{*strategyName}
	}

	var allOrders []backtest.Order
	for _, name := range selected {
		fmt.Printf("\n%s\n", strings.Repeat("=", 80))
		fmt.Printf("STRATEGY: %s\n", name)
		fmt.Printf("%s\n", strings.Repeat("=", 80))
		res, err := engine.RunStrategy(name, 0)
		if err != nil {
			log.Error("run strategy", "name", name, "err", err)
			continue
		}
		filtered := filterOrders(res.Orders, *minPrice, *maxPrice)
		printSummary(name, filtered, res.MatchCount)
		allOrders = append(allOrders, filtered...)
	}

	if len(selected) > 1 {
		fmt.Printf("\n%s\n", strings.Repeat("=", 80))
		fmt.Printf("AGGREGATE — ALL STRATEGIES\n")
		fmt.Printf("%s\n", strings.Repeat("=", 80))
		printAggregate(allOrders)
	}
}

// filterOrders applies client-side price filters for display.
// minPrice/maxPrice = 0 means no filter on that bound.
func filterOrders(orders []backtest.Order, minPrice, maxPrice float64) []backtest.Order {
	if minPrice <= 0 && maxPrice <= 0 {
		return orders
	}
	filtered := orders[:0:0]
	for _, o := range orders {
		if minPrice > 0 && o.Price < minPrice {
			continue
		}
		if maxPrice > 0 && o.Price > maxPrice {
			continue
		}
		filtered = append(filtered, o)
	}
	return filtered
}

// printSummary prints backtest results for a single strategy.
func printSummary(name string, orders []backtest.Order, both int) {
	fmt.Printf("Matches with markets: %d\n", both)
	fmt.Printf("Total signals: %d\n", len(orders))

	if len(orders) == 0 {
		fmt.Println("No orders would have been emitted.")
		return
	}

	wins, losses := 0, 0
	var totalPnL, totalInvested, totalPayout float64
	for _, o := range orders {
		if o.Won {
			wins++
			totalPayout += o.Size
		} else {
			losses++
		}
		totalPnL += o.PnL
		totalInvested += o.Size * o.Price
	}

	fmt.Printf("Wins: %d (%.1f%%)\n", wins, float64(wins)/float64(len(orders))*100)
	fmt.Printf("Losses: %d (%.1f%%)\n", losses, float64(losses)/float64(len(orders))*100)
	fmt.Printf("Total invested: $%.2f\n", totalInvested)
	fmt.Printf("Total payout: $%.2f\n", totalPayout)
	fmt.Printf("Net P&L: $%.2f\n", totalPnL)
	if totalInvested > 0 {
		fmt.Printf("ROI: %.1f%%\n", totalPnL/totalInvested*100)
	}

	var sumEdge, sumSize, sumPrice float64
	for _, o := range orders {
		sumEdge += float64(o.EdgeCents)
		sumSize += o.Size
		sumPrice += o.Price
	}
	n := float64(len(orders))
	fmt.Printf("Avg edge: %.1f cents\n", sumEdge/n)
	fmt.Printf("Avg size: %.1f\n", sumSize/n)
	fmt.Printf("Avg price: %.3f\n", sumPrice/n)

	// Risk-adjusted metrics
	var sumSqDev, sumDownside, grossWin, grossLoss float64
	var cumulative, peak, maxDD float64
	for _, o := range orders {
		dev := o.PnL - (totalPnL / n)
		sumSqDev += dev * dev
		if o.PnL < 0 {
			sumDownside += o.PnL * o.PnL
			grossLoss += -o.PnL
		} else {
			grossWin += o.PnL
		}
		cumulative += o.PnL
		if cumulative > peak {
			peak = cumulative
		}
		dd := peak - cumulative
		if dd > maxDD {
			maxDD = dd
		}
	}
	stddev := sqrtF(sumSqDev / n)
	downsideDev := sqrtF(sumDownside / n)
	var sharpe, sortino, profitFactor float64
	if stddev > 0 {
		sharpe = (totalPnL / n) / stddev
	}
	if downsideDev > 0 {
		sortino = (totalPnL / n) / downsideDev
	}
	if grossLoss > 0 {
		profitFactor = grossWin / grossLoss
	}

	fmt.Printf("\n--- Risk-Adjusted Metrics ---\n")
	fmt.Printf("Sharpe (per-trade): %.4f\n", sharpe)
	fmt.Printf("Sortino (per-trade): %.4f\n", sortino)
	fmt.Printf("Max drawdown: $%.2f\n", maxDD)
	fmt.Printf("Profit factor: %.2f\n", profitFactor)
	fmt.Printf("Std dev per trade: $%.2f\n", stddev)
	fmt.Printf("Downside dev per trade: $%.2f\n", downsideDev)

	// Per-order detail
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].Match != orders[j].Match {
			return orders[i].Match < orders[j].Match
		}
		return orders[i].SetNum < orders[j].SetNum
	})

	fmt.Printf("\n%-45s %-20s %6s %5s %6s %4s %8s\n", "match", "ctx", "price", "edge", "size", "won", "pnl")
	fmt.Println(strings.Repeat("-", 100))
	for _, o := range orders {
		wonStr := "N"
		if o.Won {
			wonStr = "Y"
		}
		fmt.Printf("%-45s %-20s %6.3f %5dc %6.1f %4s %8.2f\n",
			o.Match, o.Context, o.Price, o.EdgeCents, o.Size, wonStr, o.PnL)
	}
}

// printAggregate prints a combined summary across all strategies.
func printAggregate(orders []backtest.Order) {
	fmt.Printf("Total signals: %d\n", len(orders))

	if len(orders) == 0 {
		fmt.Println("No orders would have been emitted.")
		return
	}

	wins, losses := 0, 0
	var totalPnL, totalInvested, totalPayout float64
	for _, o := range orders {
		if o.Won {
			wins++
			totalPayout += o.Size
		} else {
			losses++
		}
		totalPnL += o.PnL
		totalInvested += o.Size * o.Price
	}

	fmt.Printf("Wins: %d (%.1f%%)\n", wins, float64(wins)/float64(len(orders))*100)
	fmt.Printf("Losses: %d (%.1f%%)\n", losses, float64(losses)/float64(len(orders))*100)
	fmt.Printf("Total invested: $%.2f\n", totalInvested)
	fmt.Printf("Total payout: $%.2f\n", totalPayout)
	fmt.Printf("Net P&L: $%.2f\n", totalPnL)
	if totalInvested > 0 {
		fmt.Printf("ROI: %.1f%%\n", totalPnL/totalInvested*100)
	}
}

func sqrtF(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Sqrt(x)
}
