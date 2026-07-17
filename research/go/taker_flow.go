package main

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"time"
)

// TakerFlow tests RQ4: does signed trade flow imbalance predict forward price?
// For each market, bucket trades into 60s windows. Compute net signed volume
// (buy-YES minus sell-YES). Correlate with 60s forward mid-price move.
// VPIN-style: informed flow should predict direction.
func init() { register(&takerFlowModule{}) }

type takerFlowModule struct{}

func (takerFlowModule) Name() string { return "taker-flow" }
func (takerFlowModule) Desc() string { return "RQ4: signed trade flow imbalance vs forward price move" }

func (m takerFlowModule) Run(db *sql.DB, args []string) {
	windowSec := 60
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &windowSec)
	}
	windowMs := int64(windowSec) * 1000

	fmt.Printf("RQ4: Taker flow toxicity (window=%ds, forward=%ds)\n\n", windowSec, windowSec)

	// Load all trades with taker_side populated, per market.
	type trade struct {
		ts     int64
		price  float64
		side   string // "yes" or "no"
		book   string // "bid" or "ask"
		volume float64
	}
	marketTrades := map[string][]trade{}

	rows, err := db.Query(`
		SELECT market_ticker, ts, price, taker_side, taker_book_side, volume
		FROM ticks
		WHERE msg_type='trade' AND taker_side IS NOT NULL AND price IS NOT NULL AND volume IS NOT NULL
		ORDER BY market_ticker, ts
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query trades: %v\n", err)
		return
	}
	defer rows.Close()

	var totalTrades int
	for rows.Next() {
		var mt, side, book string
		var ts int64
		var price, vol float64
		if err := rows.Scan(&mt, &ts, &price, &side, &book, &vol); err != nil {
			fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			return
		}
		marketTrades[mt] = append(marketTrades[mt], trade{ts, price, side, book, vol})
		totalTrades++
	}
	fmt.Printf("Loaded %d trades across %d markets\n\n", totalTrades, len(marketTrades))

	// For each market, slide 60s windows. Net signed volume = sum(vol where side=yes,book=bid) - sum(vol where side=yes,book=ask).
	// side=no,book=ask = buying NO = bearish on YES = same as selling YES.
	// side=no,book=bid = selling NO = bullish on YES = same as buying YES.
	// Signed YES flow = vol(yes,bid) + vol(no,ask) - vol(yes,ask) - vol(no,bid)
	// Wait: taker_side=yes,book=bid means someone SOLD YES (hit bid). That's bearish.
	// taker_side=yes,book=ask means someone BOUGHT YES (hit ask). That's bullish.
	// taker_side=no,book=ask means someone BOUGHT NO (hit ask) = bearish on YES.
	// taker_side=no,book=bid means someone SOLD NO (hit bid) = bullish on YES.
	// So bullish YES flow = yes_ask_buys + no_bid_sells - yes_bid_sells - no_ask_buys

	type windowResult struct {
		signedVol float64
		priceMove float64
		startTs   int64
	}

	var results []windowResult

	for mt, trades := range marketTrades {
		if len(trades) < 10 {
			continue
		}
		// Load all ticker prices for this market to measure forward move
		priceRows, err := db.Query(`
			SELECT ts, price FROM ticks
			WHERE market_ticker=? AND msg_type='ticker' AND price IS NOT NULL
			ORDER BY ts
		`, mt)
		if err != nil {
			continue
		}
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

		// For each trade window start, compute signed volume in [t, t+window]
		// and price move from t to t+window.
		for i := 0; i < len(trades); i++ {
			t0 := trades[i].ts
			t1 := t0 + windowMs

			var signedVol float64
			j := i
			for j < len(trades) && trades[j].ts < t1 {
				t := trades[j]
				bullish := (t.side == "yes" && t.book == "ask") || (t.side == "no" && t.book == "bid")
				if bullish {
					signedVol += t.volume
				} else {
					signedVol -= t.volume
				}
				j++
			}
			if signedVol == 0 {
				continue
			}

			// Find price at t0 and t1
			p0 := nearestPrice(prices, t0)
			p1 := nearestPrice(prices, t1)
			if p0 < 0 || p1 < 0 {
				continue
			}
			move := p1 - p0
			results = append(results, windowResult{signedVol, move, t0})
		}
	}

	fmt.Printf("Computed %d (signed_vol, price_move) pairs\n\n", len(results))
	if len(results) == 0 {
		fmt.Println("No data. Need more trades with taker_side populated.")
		return
	}

	// Bucket by signed_vol sign and magnitude
	bullish := 0
	bearish := 0
	var bullMoves, bearMoves []float64
	for _, r := range results {
		if r.signedVol > 0 {
			bullish++
			bullMoves = append(bullMoves, r.priceMove)
		} else {
			bearish++
			bearMoves = append(bearMoves, r.priceMove)
		}
	}

	bullAvg := avg(bullMoves)
	bearAvg := avg(bearMoves)

	fmt.Printf("Bullish flow windows: %d, avg forward move: %s\n", bullish, cents(bullAvg))
	fmt.Printf("Bearish flow windows: %d, avg forward move: %s\n", bearish, cents(bearAvg))
	fmt.Printf("Spread (bull-bear):   %s\n\n", cents(bullAvg-bearAvg))

	// Quintile analysis: sort by signed_vol, measure avg move per quintile
	sort.Slice(results, func(i, j int) bool {
		return results[i].signedVol < results[j].signedVol
	})
	qSize := len(results) / 5
	if qSize == 0 {
		qSize = 1
	}
	fmt.Println("Quintile analysis (sorted by signed volume):")
	fmt.Printf("  %-12s %-12s %-12s %s\n", "quintile", "avg_signed_vol", "avg_move", "n")
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
			avgVol += r.signedVol
			avgMove += r.priceMove
		}
		avgVol /= float64(len(bucket))
		avgMove /= float64(len(bucket))
		fmt.Printf("  Q%d          %-12.1f %-12s %d\n", q+1, avgVol, cents(avgMove), len(bucket))
	}

	// Pearson correlation
	var sumV, sumM, sumVM, sumV2, sumM2 float64
	n := float64(len(results))
	for _, r := range results {
		sumV += r.signedVol
		sumM += r.priceMove
		sumVM += r.signedVol * r.priceMove
		sumV2 += r.signedVol * r.signedVol
		sumM2 += r.priceMove * r.priceMove
	}
	meanV := sumV / n
	meanM := sumM / n
	cov := sumVM/n - meanV*meanM
	stdV := sqrt(sumV2/n - meanV*meanV)
	stdM := sqrt(sumM2/n - meanM*meanM)
	pearson := 0.0
	if stdV > 0 && stdM > 0 {
		pearson = cov / (stdV * stdM)
	}
	fmt.Printf("\nPearson correlation (signed_vol, forward_move): %.4f\n", pearson)
	fmt.Printf("Signal strength: %s per unit of signed volume\n", cents(cov/(stdV*stdV)))

	_ = os.Stdout
	_ = time.Now
}

// pt is a price timestamp tuple for nearestPrice lookups.
type pt struct{ ts int64; p float64 }

// nearestPrice returns the price closest to target ts, or -1 if none within 5s.
func nearestPrice(prices []pt, target int64) float64 {
	if len(prices) == 0 {
		return -1
	}
	// Binary search
	lo, hi := 0, len(prices)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if prices[mid].ts < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	// Check lo and lo-1
	best := lo
	if lo > 0 && abs(prices[lo-1].ts-target) < abs(prices[lo].ts-target) {
		best = lo - 1
	}
	if abs(prices[best].ts-target) > 5000 {
		return -1
	}
	return prices[best].p
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func avg(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func sqrt(x float64) float64 {
	if x < 0 {
		return 0
	}
	// Newton's method
	g := x / 2
	for i := 0; i < 20; i++ {
		g = (g + x/g) / 2
	}
	return g
}
