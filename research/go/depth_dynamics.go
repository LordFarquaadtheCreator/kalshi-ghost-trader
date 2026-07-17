package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// DepthDynamics tests RQ9: multi-level orderbook depth as volatility predictor.
// Existing study used top-of-book only. Snapshots have full depth ladder.
// Thin depth at levels 2-5 = fragile quote = larger forward price jump.
func init() { register(&depthDynamicsModule{}) }

type depthDynamicsModule struct{}

func (d depthDynamicsModule) Name() string { return "depth-dynamics" }
func (d depthDynamicsModule) Desc() string { return "RQ9: multi-level orderbook depth vs forward volatility" }

func (d depthDynamicsModule) Run(db *sql.DB, args []string) {
	fmt.Println("RQ9: Orderbook depth dynamics")
	fmt.Println("=============================")

	// Load orderbook snapshots — they have full depth ladder
	rows, err := db.Query(`
		SELECT market_ticker, ts, payload
		FROM orderbook_events
		WHERE msg_type='orderbook_snapshot'
		ORDER BY ts
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query snapshots: %v\n", err)
		return
	}
	defer rows.Close()

	type snapshot struct {
		MarketTicker string
		Ts           int64
		YesBids      [][2]float64 // [[price, size], ...]
		YesAsks      [][2]float64
		Depth5Bid    float64 // cumulative size at top 5 bid levels
		Depth5Ask    float64
		Imbalance    float64 // (depth5Bid - depth5Ask) / (depth5Bid + depth5Ask)
	}

	var snapshots []snapshot
	for rows.Next() {
		var mt string
		var ts int64
		var payload string
		rows.Scan(&mt, &ts, &payload)

		var raw struct {
			Msg struct {
				YesDollarsFP [][2]string `json:"yes_dollars_fp"`
				NoDollarsFP  [][2]string `json:"no_dollars_fp"`
			} `json:"msg"`
		}
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			continue
		}

		snap := snapshot{MarketTicker: mt, Ts: ts}
		// yes_dollars_fp: [[price, size], ...] — bids are below mid, asks above
		// Sort by price to separate bids and asks
		var allLevels [][2]float64
		for _, lvl := range raw.Msg.YesDollarsFP {
			if len(lvl) < 2 {
				continue
			}
			var p, sz float64
			fmt.Sscanf(lvl[0], "%f", &p)
			fmt.Sscanf(lvl[1], "%f", &sz)
			if sz > 0 {
				allLevels = append(allLevels, [2]float64{p, sz})
			}
		}
		if len(allLevels) < 2 {
			continue
		}
		// Sort by price descending — top bids first
		sort.Slice(allLevels, func(i, j int) bool {
			return allLevels[i][0] > allLevels[j][0]
		})
		// First few are asks (highest), then bids. Actually Kalshi format:
		// yes_dollars_fp lists all levels. We need to determine mid from yes/no.
		// Simpler: treat first 5 as one side, last 5 as other.
		// Actually: bids are buy orders (below mid), asks are sell orders (above mid).
		// In yes_dollars_fp, higher price = ask (selling YES expensive), lower = bid.
		// Split at midpoint of sorted list.
		mid := len(allLevels) / 2
		for i := 0; i < mid && i < 5; i++ {
			snap.YesAsks = append(snap.YesAsks, allLevels[i])
			snap.Depth5Ask += allLevels[i][1]
		}
		for i := mid; i < len(allLevels) && (i-mid) < 5; i++ {
			snap.YesBids = append(snap.YesBids, allLevels[i])
			snap.Depth5Bid += allLevels[i][1]
		}
		if snap.Depth5Bid+snap.Depth5Ask > 0 {
			snap.Imbalance = (snap.Depth5Bid - snap.Depth5Ask) / (snap.Depth5Bid + snap.Depth5Ask)
		}
		snapshots = append(snapshots, snap)
	}
	fmt.Printf("Loaded %d orderbook snapshots\n", len(snapshots))
	if len(snapshots) == 0 {
		fmt.Println("No snapshots. Need orderbook_snapshot messages.")
		return
	}

	// For each snapshot, measure forward 60s price volatility (max-min of ticker prices)
	type result struct {
		Imbalance    float64
		Depth5Total  float64
		ForwardVol   float64 // max - min price in 60s after snapshot
		ForwardMove  float64 // price_60s - price_0
	}

	var results []result
	for _, snap := range snapshots {
		// Get ticker prices in [snap.ts, snap.ts+60000]
		priceRows, err := db.Query(`
			SELECT price FROM ticks
			WHERE market_ticker=? AND msg_type='ticker' AND price IS NOT NULL
			  AND ts BETWEEN ? AND ?
			ORDER BY ts
		`, snap.MarketTicker, snap.Ts, snap.Ts+60000)
		if err != nil {
			continue
		}
		var prices []float64
		for priceRows.Next() {
			var p float64
			priceRows.Scan(&p)
			prices = append(prices, p)
		}
		priceRows.Close()
		if len(prices) < 2 {
			continue
		}
		minP, maxP := prices[0], prices[0]
		for _, p := range prices {
			if p < minP {
				minP = p
			}
			if p > maxP {
				maxP = p
			}
		}
		results = append(results, result{
			Imbalance:   snap.Imbalance,
			Depth5Total: snap.Depth5Bid + snap.Depth5Ask,
			ForwardVol:  maxP - minP,
			ForwardMove: prices[len(prices)-1] - prices[0],
		})
	}

	fmt.Printf("Snapshots with forward price data: %d\n\n", len(results))
	if len(results) == 0 {
		fmt.Println("No forward price data after snapshots.")
		return
	}

	// Quintile by depth5 total: thin book → more volatility?
	sort.Slice(results, func(i, j int) bool {
		return results[i].Depth5Total < results[j].Depth5Total
	})
	fmt.Println("Forward volatility by depth quintile (thin → deep):")
	fmt.Printf("  %-12s %-8s %-14s %-14s\n", "depth_range", "n", "avg_vol_60s", "avg_move_60s")
	qSize := len(results) / 5
	if qSize == 0 {
		qSize = 1
	}
	for q := 0; q < 5; q++ {
		start := q * qSize
		end := start + qSize
		if q == 4 {
			end = len(results)
		}
		if start >= len(results) {
			break
		}
		bucket := results[start:end]
		avgVol := 0.0
		avgMove := 0.0
		for _, r := range bucket {
			avgVol += r.ForwardVol
			avgMove += r.ForwardMove
		}
		avgVol /= float64(len(bucket))
		avgMove /= float64(len(bucket))
		fmt.Printf("  %.0f-%.0f    %-8d %-14s %-14s\n",
			bucket[0].Depth5Total, bucket[len(bucket)-1].Depth5Total,
			len(bucket), cents(avgVol), cents(avgMove))
	}

	// Imbalance quintile vs forward move
	sort.Slice(results, func(i, j int) bool {
		return results[i].Imbalance < results[j].Imbalance
	})
	fmt.Println("\nForward move by imbalance quintile (heavy ask → heavy bid):")
	fmt.Printf("  %-12s %-8s %-14s %-14s\n", "imb_range", "n", "avg_move", "avg_vol")
	for q := 0; q < 5; q++ {
		start := q * qSize
		end := start + qSize
		if q == 4 {
			end = len(results)
		}
		if start >= len(results) {
			break
		}
		bucket := results[start:end]
		avgMove := 0.0
		avgVol := 0.0
		for _, r := range bucket {
			avgMove += r.ForwardMove
			avgVol += r.ForwardVol
		}
		avgMove /= float64(len(bucket))
		avgVol /= float64(len(bucket))
		fmt.Printf("  %.2f-%.2f    %-8d %-14s %-14s\n",
			bucket[0].Imbalance, bucket[len(bucket)-1].Imbalance,
			len(bucket), cents(avgMove), cents(avgVol))
	}

	// Correlation: depth vs volatility
	var sumD, sumV, sumDV, sumD2, sumV2 float64
	n := float64(len(results))
	for _, r := range results {
		sumD += r.Depth5Total
		sumV += r.ForwardVol
		sumDV += r.Depth5Total * r.ForwardVol
		sumD2 += r.Depth5Total * r.Depth5Total
		sumV2 += r.ForwardVol * r.ForwardVol
	}
	meanD := sumD / n
	meanV := sumV / n
	cov := sumDV/n - meanD*meanV
	stdD := sqrt(sumD2/n - meanD*meanD)
	stdV := sqrt(sumV2/n - meanV*meanV)
	pearson := 0.0
	if stdD > 0 && stdV > 0 {
		pearson = cov / (stdD * stdV)
	}
	fmt.Printf("\nCorrelation (depth, forward_vol): %.4f\n", pearson)
	fmt.Printf("Negative = thin book predicts higher vol (expected)\n")

	_ = os.Stdout
}
