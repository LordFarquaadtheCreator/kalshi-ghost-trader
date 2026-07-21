// Command pricebands runs all backtest strategies, buckets resolved orders
// into fixed price bands, and writes per-day + aggregate tables to a text file.
// Output goes to pricebands_output.txt by default (or -out path).
//
// Usage:
//
//	go run ./cmd/pricebands
//	go run ./cmd/pricebands -day 2026-07-17
//	go run ./cmd/pricebands -out /tmp/bands.txt
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/appconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

var out *os.File

type band struct {
	lo, hi float64
}

var bands = []band{
	{0.01, 0.05},
	{0.05, 0.10},
	{0.10, 0.15},
	{0.15, 0.20},
	{0.20, 0.30},
	{0.30, 0.40},
	{0.40, 0.50},
	{0.50, 0.60},
	{0.60, 0.70},
	{0.70, 0.80},
	{0.80, 0.90},
	{0.90, 0.99},
}

func bandLabel(b band) string {
	return fmt.Sprintf("%.2f-%.2f", b.lo, b.hi)
}

func findBand(price float64) int {
	for i, b := range bands {
		if price >= b.lo && price < b.hi {
			return i
		}
	}
	return -1
}

type agg struct {
	strategy  string
	bandIdx   int
	bandLabel string
	n         int
	wins      int
	netPnL    float64
	invested  float64
	edgeSum   float64
}

func (a *agg) winRate() float64 {
	if a.n == 0 {
		return 0
	}
	return float64(a.wins) / float64(a.n) * 100
}

func (a *agg) roi() float64 {
	if a.invested <= 0 {
		return 0
	}
	return a.netPnL / a.invested * 100
}

func (a *agg) avgEdge() float64 {
	if a.n == 0 {
		return 0
	}
	return a.edgeSum / float64(a.n)
}

func main() {
	minN := flag.Int("min-n", 5, "minimum sample size to show in best-bands table")
	minWR := flag.Float64("min-wr", 55, "minimum win rate %% to show in best-bands table")
	dayFilter := flag.String("day", "", "filter to specific day YYYY-MM-DD; empty = all days")
	outPath := flag.String("out", "pricebands_output.txt", "output file path")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var err error
	out, err = os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output file: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	// Load config (sets config.Cfg global)
	appCfg, err := appconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "appconfig load: %v\n", err)
		os.Exit(1)
	}
	db, err := store.New(context.Background(), appCfg.DBDSN, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init: %v\n", err)
		os.Exit(1)
	}
	if _, err := config.Load(db); err != nil {
		fmt.Fprintf(os.Stderr, "config load: %v\n", err)
		os.Exit(1)
	}
	db.Close()

	fmt.Fprintln(os.Stderr, "Loading data + running all strategies...")
	engine, err := backtest.NewEngine(log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer engine.Close()

	results, err := engine.RunAll(0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Writing output to %s\n", *outPath)

	// Group orders by day
	type dayKey struct{ day string }
	type sbKey struct {
		strat   string
		bandIdx int
	}
	dayAggs := map[string]map[sbKey]*agg{}
	dayOrderCounts := map[string]int{}

	for _, res := range results {
		for _, o := range res.Orders {
			if o.Price < 0.01 || o.Price >= 0.99 {
				continue
			}
			bi := findBand(o.Price)
			if bi < 0 {
				continue
			}
			day := tsToDay(o.TS)
			if *dayFilter != "" && day != *dayFilter {
				continue
			}
			if dayAggs[day] == nil {
				dayAggs[day] = map[sbKey]*agg{}
			}
			k := sbKey{res.Name, bi}
			a := dayAggs[day][k]
			if a == nil {
				a = &agg{strategy: res.Name, bandIdx: bi, bandLabel: bandLabel(bands[bi])}
				dayAggs[day][k] = a
			}
			a.n++
			if o.Won {
				a.wins++
			}
			a.netPnL += o.PnL
			a.invested += o.Size * o.Price
			a.edgeSum += float64(o.EdgeCents)
			dayOrderCounts[day]++
		}
	}

	// Sort days
	days := make([]string, 0, len(dayAggs))
	for d := range dayAggs {
		days = append(days, d)
	}
	sort.Strings(days)

	if len(days) == 0 {
		fmt.Fprintln(out, "No orders found"+dayFilterMsg(*dayFilter))
		os.Exit(0)
	}

	for _, day := range days {
		aggs := dayAggs[day]
		flat := make([]*agg, 0, len(aggs))
		for _, a := range aggs {
			flat = append(flat, a)
		}

		// === Table 1: per-strategy-per-band ===
		sort.Slice(flat, func(i, j int) bool {
			if flat[i].n != flat[j].n {
				return flat[i].n > flat[j].n
			}
			return flat[i].winRate() > flat[j].winRate()
		})

		fmt.Fprintf(out, "\n%s\n", strings.Repeat("=", 110))
		fmt.Fprintf(out, "%s — PRICE BANDS (sorted by N desc, WinRate desc) | %d orders\n", day, dayOrderCounts[day])
		fmt.Fprintf(out, "%s\n\n", strings.Repeat("=", 110))
		fmt.Fprintf(out, "%-25s %-12s %6s %5s %7s %10s %8s %8s\n",
			"Strategy", "Band", "N", "Wins", "WinRate", "NetPnL", "ROI%", "AvgEdge")
		fmt.Fprintln(out, strings.Repeat("-", 110))
		for _, a := range flat {
			if a.n == 0 {
				continue
			}
			fmt.Fprintf(out, "%-25s %-12s %6d %5d %6.1f%% %10.2f %7.1f%% %8.1f\n",
				a.strategy, a.bandLabel, a.n, a.wins, a.winRate(),
				a.netPnL, a.roi(), a.avgEdge())
		}

		// === Table 2: cross-strategy band totals ===
		bandTotals := make([]*agg, len(bands))
		for i := range bands {
			bandTotals[i] = &agg{bandIdx: i, bandLabel: bandLabel(bands[i])}
		}
		for _, a := range flat {
			bt := bandTotals[a.bandIdx]
			bt.n += a.n
			bt.wins += a.wins
			bt.netPnL += a.netPnL
			bt.invested += a.invested
			bt.edgeSum += a.edgeSum
		}

		sort.Slice(bandTotals, func(i, j int) bool {
			if bandTotals[i].n != bandTotals[j].n {
				return bandTotals[i].n > bandTotals[j].n
			}
			return bandTotals[i].winRate() > bandTotals[j].winRate()
		})

		fmt.Fprintf(out, "\n%s\n", strings.Repeat("=", 80))
		fmt.Fprintf(out, "%s — CROSS-STRATEGY BAND TOTALS\n", day)
		fmt.Fprintf(out, "%s\n\n", strings.Repeat("=", 80))
		fmt.Fprintf(out, "%-12s %6s %5s %7s %10s %8s\n", "Band", "N", "Wins", "WinRate", "NetPnL", "ROI%")
		fmt.Fprintln(out, strings.Repeat("-", 80))
		for _, a := range bandTotals {
			if a.n == 0 {
				continue
			}
			fmt.Fprintf(out, "%-12s %6d %5d %6.1f%% %10.2f %7.1f%%\n",
				a.bandLabel, a.n, a.wins, a.winRate(), a.netPnL, a.roi())
		}

		// === Table 3: best bands ===
		var best []*agg
		for _, a := range flat {
			if a.n >= *minN && a.winRate() >= *minWR {
				best = append(best, a)
			}
		}
		sort.Slice(best, func(i, j int) bool {
			if best[i].winRate() != best[j].winRate() {
				return best[i].winRate() > best[j].winRate()
			}
			return best[i].n > best[j].n
		})

		fmt.Fprintf(out, "\n%s\n", strings.Repeat("=", 110))
		fmt.Fprintf(out, "%s — BEST BANDS (N>=%d, WinRate>=%.0f%%)\n", day, *minN, *minWR)
		fmt.Fprintf(out, "%s\n\n", strings.Repeat("=", 110))
		fmt.Fprintf(out, "%-25s %-12s %6s %5s %7s %10s %8s %8s\n",
			"Strategy", "Band", "N", "Wins", "WinRate", "NetPnL", "ROI%", "AvgEdge")
		fmt.Fprintln(out, strings.Repeat("-", 110))
		if len(best) == 0 {
			fmt.Fprintln(out, "(no bands meet criteria)")
		}
		for _, a := range best {
			fmt.Fprintf(out, "%-25s %-12s %6d %5d %6.1f%% %10.2f %7.1f%% %8.1f\n",
				a.strategy, a.bandLabel, a.n, a.wins, a.winRate(),
				a.netPnL, a.roi(), a.avgEdge())
		}
	}

	// Cross-day summary: track tier-1 bands across days
	fmt.Fprintf(out, "\n%s\n", strings.Repeat("=", 110))
	fmt.Fprintf(out, "CROSS-DAY SUMMARY — tier-1 bands (excluding fadelongshot*, nofade)\n")
	fmt.Fprintf(out, "%s\n\n", strings.Repeat("=", 110))

	type trackKey struct{ strat, bandLabel string }
	trackAggs := map[trackKey]*agg{}
	for _, day := range days {
		for _, a := range dayAggs[day] {
			if strings.HasPrefix(a.strategy, "fadelongshot") || a.strategy == "nofade" {
				continue
			}
			if a.n < *minN || a.winRate() < *minWR {
				continue
			}
			k := trackKey{a.strategy, a.bandLabel}
			ta := trackAggs[k]
			if ta == nil {
				ta = &agg{strategy: a.strategy, bandLabel: a.bandLabel}
				trackAggs[k] = ta
			}
			ta.n += a.n
			ta.wins += a.wins
			ta.netPnL += a.netPnL
			ta.invested += a.invested
			ta.edgeSum += a.edgeSum
		}
	}
	trackFlat := make([]*agg, 0, len(trackAggs))
	for _, a := range trackAggs {
		trackFlat = append(trackFlat, a)
	}
	sort.Slice(trackFlat, func(i, j int) bool {
		if trackFlat[i].winRate() != trackFlat[j].winRate() {
			return trackFlat[i].winRate() > trackFlat[j].winRate()
		}
		return trackFlat[i].n > trackFlat[j].n
	})
	fmt.Fprintf(out, "%-25s %-12s %6s %5s %7s %10s %8s %8s\n",
		"Strategy", "Band", "N", "Wins", "WinRate", "NetPnL", "ROI%", "AvgEdge")
	fmt.Fprintln(out, strings.Repeat("-", 110))
	if len(trackFlat) == 0 {
		fmt.Fprintln(out, "(no bands meet criteria on any day)")
	}
	for _, a := range trackFlat {
		fmt.Fprintf(out, "%-25s %-12s %6d %5d %6.1f%% %10.2f %7.1f%% %8.1f\n",
			a.strategy, a.bandLabel, a.n, a.wins, a.winRate(),
			a.netPnL, a.roi(), a.avgEdge())
	}

	// Per-day tier-1 presence matrix
	fmt.Fprintf(out, "\n%s\n", strings.Repeat("=", 110))
	fmt.Fprintf(out, "TIER-1 PRESENCE BY DAY (N>=%d, WR>=%.0f%%, excluding fadelongshot*, nofade)\n", *minN, *minWR)
	fmt.Fprintf(out, "%s\n\n", strings.Repeat("=", 110))
	fmt.Fprintf(out, "%-12s %6s %8s %8s %8s %8s %8s %8s\n",
		"Day", "Orders", "crossArb", "cnvxPool", "setpoint", "set1win", "brkpoint", "matchPt")
	fmt.Fprintln(out, strings.Repeat("-", 110))
	for _, day := range days {
		counts := map[string]int{}
		for _, a := range dayAggs[day] {
			if strings.HasPrefix(a.strategy, "fadelongshot") || a.strategy == "nofade" {
				continue
			}
			if a.n < *minN || a.winRate() < *minWR {
				continue
			}
			short := shortName(a.strategy)
			counts[short] += a.n
		}
		fmt.Fprintf(out, "%-12s %6d %8s %8s %8s %8s %8s %8s\n",
			day, dayOrderCounts[day],
			intStr(counts["crossArb"]), intStr(counts["cnvxPool"]),
			intStr(counts["setpoint"]), intStr(counts["set1win"]),
			intStr(counts["brkpoint"]), intStr(counts["matchPt"]))
	}

	totalOrders := 0
	for _, res := range results {
		totalOrders += len(res.Orders)
	}
	fmt.Fprintf(out, "\nTotal strategies: %d | Total orders: %d | Days: %d | Bands: %d\n",
		len(results), totalOrders, len(days), len(bands))
}

func tsToDay(ts int64) string {
	var t time.Time
	if ts > 1e12 {
		t = time.UnixMilli(ts)
	} else {
		t = time.Unix(ts, 0)
	}
	return t.UTC().Format("2006-01-02")
}

func dayFilterMsg(d string) string {
	if d == "" {
		return ""
	}
	return fmt.Sprintf(" for day %s", d)
}

func shortName(s string) string {
	switch {
	case strings.HasPrefix(s, "cross-arb"):
		return "crossArb"
	case strings.HasPrefix(s, "convexpool"):
		return "cnvxPool"
	case strings.HasPrefix(s, "setpoint"):
		return "setpoint"
	case strings.HasPrefix(s, "set1winner"):
		return "set1win"
	case strings.HasPrefix(s, "breakpoint"):
		return "brkpoint"
	case strings.HasPrefix(s, "matchpoint"):
		return "matchPt"
	default:
		return s
	}
}

func intStr(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}
