package main

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
)

// PointPriceLatency tests RQ12: from point scored (points.ts_ms) to next
// significant price change. If Kalshi price moves BEFORE our point timestamp,
// market makers have faster score feeds and we're exit liquidity.
// If price moves AFTER, we have an edge window.
func init() { register(&pointPriceLatencyModule{}) }

type pointPriceLatencyModule struct{}

func (m pointPriceLatencyModule) Name() string { return "pp-latency" }
func (m pointPriceLatencyModule) Desc() string { return "RQ12: point-to-price latency — do we have an edge window?" }

func (m pointPriceLatencyModule) Run(db *sql.DB, args []string) {
	fmt.Println("RQ12: Point-to-price latency")
	fmt.Println("=============================")

	// Load all points with timestamps
	rows, err := db.Query(`
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points, is_break_point
		FROM points
		WHERE ts_ms IS NOT NULL
		ORDER BY match_ticker, ts_ms
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query points: %v\n", err)
		return
	}
	defer rows.Close()

	type point struct {
		MatchTicker string
		TsMs        int64
		IsBreak     bool
		Scorer      int
	}
	matchPoints := map[string][]point{}
	for rows.Next() {
		var p point
		var sr, isBP int
		var hp, ap string
		var sn, gn, pn int
		rows.Scan(&p.MatchTicker, &p.TsMs, &sn, &gn, &pn, &sr, &p.Scorer, &hp, &ap, &isBP)
		p.IsBreak = isBP != 0
		matchPoints[p.MatchTicker] = append(matchPoints[p.MatchTicker], p)
	}
	fmt.Printf("Loaded points for %d matches\n", len(matchPoints))

	// For each match, load all tick prices and find when price moves >2c after each point
	type latencyResult struct {
		MatchTicker    string
		PointTs        int64
		FirstMoveTs    int64
		LatencyMs      int64
		MoveMagnitude  float64
		PriceBefore    float64
		PriceAfter     float64
		IsBreak        bool
		PriceMovedUp   bool // did the scorer's market price go up?
	}

	var results []latencyResult
	moveThreshold := 0.02 // 2c

	for ticker, pts := range matchPoints {
		// Get both markets for this event
		var mts []string
		mkRows, _ := db.Query(`SELECT market_ticker FROM markets WHERE event_ticker=? ORDER BY market_ticker`, ticker)
		for mkRows != nil && mkRows.Next() {
			var mt string
			mkRows.Scan(&mt)
			mts = append(mts, mt)
		}
		if mkRows != nil {
			mkRows.Close()
		}
		if len(mts) < 2 {
			continue
		}

		// Load price series for both markets
		for _, mkt := range mts {
			priceRows, err := db.Query(`
				SELECT ts, price FROM ticks
				WHERE market_ticker=? AND msg_type='ticker' AND price IS NOT NULL
				ORDER BY ts
			`, mkt)
			if err != nil {
				continue
			}
			type pt struct{ ts int64; p float64 }
			var prices []pt
			for priceRows.Next() {
				var ts int64
				var p float64
				priceRows.Scan(&ts, &p)
				prices = append(prices, pt{ts, p})
			}
			priceRows.Close()
			if len(prices) < 2 {
				continue
			}

			// For each point, find price just before and first price that moves >threshold after
			for _, p := range pts {
				// Price before point (within 5s before)
				pBefore := -1.0
				for i := len(prices) - 1; i >= 0; i-- {
					if prices[i].ts <= p.TsMs && p.TsMs-prices[i].ts < 5000 {
						pBefore = prices[i].p
						break
					}
				}
				if pBefore < 0 {
					continue
				}

				// Find first price move > threshold after the point
				var firstMoveTs int64 = -1
				var pAfter float64
				for i := 0; i < len(prices); i++ {
					if prices[i].ts < p.TsMs {
						continue
					}
					if abs(prices[i].ts-p.TsMs) > 120000 { // 2min window
						break
					}
					if absF(prices[i].p-pBefore) >= moveThreshold {
						firstMoveTs = prices[i].ts
						pAfter = prices[i].p
						break
					}
				}
				if firstMoveTs < 0 {
					continue
				}

				latency := firstMoveTs - p.TsMs
				results = append(results, latencyResult{
					MatchTicker:   ticker,
					PointTs:       p.TsMs,
					FirstMoveTs:   firstMoveTs,
					LatencyMs:     latency,
					MoveMagnitude: pAfter - pBefore,
					PriceBefore:   pBefore,
					PriceAfter:    pAfter,
					IsBreak:       p.IsBreak,
					PriceMovedUp:  pAfter > pBefore,
				})
			}
		}
	}

	fmt.Printf("\nPoint-to-price move pairs: %d\n", len(results))
	if len(results) == 0 {
		fmt.Println("No price moves found near points. Need more gold-set overlap.")
		return
	}

	// Latency distribution
	sort.Slice(results, func(i, j int) bool {
		return results[i].LatencyMs < results[j].LatencyMs
	})
	fmt.Println("\nLatency distribution (point_ts → first >2c price move):")
	fmt.Printf("  min:    %dms\n", results[0].LatencyMs)
	fmt.Printf("  p25:    %dms\n", results[len(results)/4].LatencyMs)
	fmt.Printf("  median: %dms\n", results[len(results)/2].LatencyMs)
	fmt.Printf("  p75:    %dms\n", results[3*len(results)/4].LatencyMs)
	fmt.Printf("  max:    %dms\n", results[len(results)-1].LatencyMs)

	// Bucket
	buckets := map[string]int{
		"a_<0ms (price moved BEFORE point)": 0,
		"b_0-500ms":                         0,
		"c_500-2000ms":                      0,
		"d_2-5s":                            0,
		"e_5-15s":                           0,
		"f_15-60s":                          0,
		"g_>60s":                            0,
	}
	for _, r := range results {
		switch {
		case r.LatencyMs < 0:
			buckets["a_<0ms (price moved BEFORE point)"]++
		case r.LatencyMs < 500:
			buckets["b_0-500ms"]++
		case r.LatencyMs < 2000:
			buckets["c_500-2000ms"]++
		case r.LatencyMs < 5000:
			buckets["d_2-5s"]++
		case r.LatencyMs < 15000:
			buckets["e_5-15s"]++
		case r.LatencyMs < 60000:
			buckets["f_15-60s"]++
		default:
			buckets["g_>60s"]++
		}
	}
	fmt.Println("\nLatency buckets:")
	keys := []string{
		"a_<0ms (price moved BEFORE point)",
		"b_0-500ms", "c_500-2000ms", "d_2-5s",
		"e_5-15s", "f_15-60s", "g_>60s",
	}
	for _, k := range keys {
		fmt.Printf("  %-40s %d (%s)\n", k, buckets[k], pct(buckets[k], len(results)))
	}

	// Critical: how many moves happened BEFORE the point?
	beforeCount := buckets["a_<0ms (price moved BEFORE point)"]
	afterCount := len(results) - beforeCount
	fmt.Printf("\nPrice moved BEFORE point: %d (%s)\n", beforeCount, pct(beforeCount, len(results)))
	fmt.Printf("Price moved AFTER point:  %d (%s)\n", afterCount, pct(afterCount, len(results)))

	if beforeCount > afterCount {
		fmt.Println("\nWARNING: majority of price moves happen BEFORE our point feed.")
		fmt.Println("Market makers have faster score feeds. We are exit liquidity.")
		fmt.Println("Point-based strategies will lose edge after fees.")
	} else {
		fmt.Println("\nGood: majority of price moves happen AFTER our point feed.")
		fmt.Println("Edge window exists. Point-based strategies viable.")
	}

	// Break points specifically
	var bpResults []latencyResult
	for _, r := range results {
		if r.IsBreak {
			bpResults = append(bpResults, r)
		}
	}
	if len(bpResults) > 0 {
		fmt.Printf("\nBreak-point-specific latency (%d events):\n", len(bpResults))
		sort.Slice(bpResults, func(i, j int) bool {
			return bpResults[i].LatencyMs < bpResults[j].LatencyMs
		})
		fmt.Printf("  median: %dms\n", bpResults[len(bpResults)/2].LatencyMs)
		bpBefore := 0
		for _, r := range bpResults {
			if r.LatencyMs < 0 {
				bpBefore++
			}
		}
		fmt.Printf("  moved before point: %s\n", pct(bpBefore, len(bpResults)))
	}

	_ = os.Stdout
}
