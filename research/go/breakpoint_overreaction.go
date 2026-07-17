package main

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
)

// BreakpointOverreaction tests RQ7: does Kalshi over-react to break conversions
// like Betfair literature documents? For each break conversion in gold-set
// matches, measure price move before and after. Test fade-the-breaker strategy.
func init() { register(&breakpointOverreactionModule{}) }

type breakpointOverreactionModule struct{}

func (b breakpointOverreactionModule) Name() string { return "bp-overreaction" }
func (b breakpointOverreactionModule) Desc() string { return "RQ7: break conversion price over-reaction (Betfair pattern on Kalshi?)" }

func (m breakpointOverreactionModule) Run(db *sql.DB, args []string) {
	fmt.Println("RQ7: Break-point over-reaction (Betfair pattern test)")
	fmt.Println("=====================================================")

	// Find break conversions: is_break_point=1 AND server != scorer (returner won = break converted)
	// Join with ticks to get market price before and after.
	rows, err := db.Query(`
		SELECT p.match_ticker, p.ts_ms, p.set_number, p.game_number,
		       p.server, p.scorer, p.home_points, p.away_points,
		       p.home_games, p.away_games
		FROM points p
		WHERE p.is_break_point=1
		  AND p.server != p.scorer
		  AND p.ts_ms IS NOT NULL
		  AND p.home_points NOT IN ('A') AND p.away_points NOT IN ('A')
		ORDER BY p.ts_ms
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query break conversions: %v\n", err)
		return
	}
	defer rows.Close()

	type breakEvent struct {
		MatchTicker string
		TsMs        int64
		SetNum      int
		GameNum     int
		Server      int
		Scorer      int
	}
	var breaks []breakEvent
	for rows.Next() {
		var b breakEvent
		var hp, ap string
		var hg, ag int
		rows.Scan(&b.MatchTicker, &b.TsMs, &b.SetNum, &b.GameNum,
			&b.Server, &b.Scorer, &hp, &ap, &hg, &ag)
		breaks = append(breaks, b)
	}
	fmt.Printf("\nBreak conversions found: %d\n", len(breaks))

	if len(breaks) == 0 {
		fmt.Println("No break conversions with timestamps. Need more gold-set data.")
		return
	}

	// For each break, find the market of the breaker (scorer) and measure price move
	// 30s before and 60s after the break.
	type result struct {
		MatchTicker   string
		BreakTs       int64
		PriceBefore   float64 // 30s before
		PriceAt       float64 // at break
		PriceAfter30  float64 // 30s after
		PriceAfter60  float64 // 60s after
		PriceAfter120 float64 // 120s after
		SpikeBefore   float64 // price_at - price_before (move into break)
		DriftAfter30  float64 // price_after30 - price_at (immediate drift)
		DriftAfter60  float64
		DriftAfter120 float64
	}

	var results []result
	for _, b := range breaks {
		// Find the breaker's market. Scorer=1 → home player's market, Scorer=2 → away.
		// Markets are ordered alphabetically by market_ticker. Home = first player in title.
		// Simpler: get both markets for the event, pick by player name match later.
		// For now, just get all ticks for both markets of this event near the break time.
		var marketTickers []string
		mkRows, err := db.Query(`
			SELECT market_ticker FROM markets WHERE event_ticker=?
			ORDER BY market_ticker
		`, b.MatchTicker)
		if err != nil {
			continue
		}
		for mkRows.Next() {
			var mt string
			mkRows.Scan(&mt)
			marketTickers = append(marketTickers, mt)
		}
		mkRows.Close()
		if len(marketTickers) < 2 {
			continue
		}
		// Scorer 1 = home = first market (alphabetical = first player typically)
		// This is approximate — proper mapping needs title parsing.
		breakerMkt := marketTickers[0]
		if b.Scorer == 2 {
			breakerMkt = marketTickers[1]
		}

		// Get prices at -30s, 0, +30s, +60s, +120s
		pBefore := priceAt(db, breakerMkt, b.TsMs-30000)
		pAt := priceAt(db, breakerMkt, b.TsMs)
		p30 := priceAt(db, breakerMkt, b.TsMs+30000)
		p60 := priceAt(db, breakerMkt, b.TsMs+60000)
		p120 := priceAt(db, breakerMkt, b.TsMs+120000)

		if pBefore < 0 || pAt < 0 {
			continue
		}

		r := result{
			MatchTicker:   b.MatchTicker,
			BreakTs:       b.TsMs,
			PriceBefore:   pBefore,
			PriceAt:       pAt,
			PriceAfter30:  p30,
			PriceAfter60:  p60,
			PriceAfter120: p120,
			SpikeBefore:   pAt - pBefore,
			DriftAfter30:  p30 - pAt,
			DriftAfter60:  p60 - pAt,
			DriftAfter120: p120 - pAt,
		}
		// Normalize: if breaker's price went UP (break is good for breaker), spike is positive.
		// Fade-the-breaker = sell breaker's YES after spike. Profit if drift is negative (reversion).
		results = append(results, r)
	}

	fmt.Printf("Breaks with price data: %d\n\n", len(results))
	if len(results) == 0 {
		fmt.Println("No breaks with tick coverage. Need more gold-set overlap.")
		return
	}

	// Summary stats
	var sumSpike, sumDrift30, sumDrift60, sumDrift120 float64
	var revert30, revert60, revert120 int
	for _, r := range results {
		sumSpike += r.SpikeBefore
		sumDrift30 += r.DriftAfter30
		sumDrift60 += r.DriftAfter60
		sumDrift120 += r.DriftAfter120
		// Reversion = drift opposite to spike
		if r.SpikeBefore > 0 && r.DriftAfter30 < 0 {
			revert30++
		}
		if r.SpikeBefore > 0 && r.DriftAfter60 < 0 {
			revert60++
		}
		if r.SpikeBefore > 0 && r.DriftAfter120 < 0 {
			revert120++
		}
	}
	n := float64(len(results))
	fmt.Printf("Avg spike into break (30s before → at):  %s\n", cents(sumSpike/n))
	fmt.Printf("Avg drift after break (at → +30s):       %s\n", cents(sumDrift30/n))
	fmt.Printf("Avg drift after break (at → +60s):       %s\n", cents(sumDrift60/n))
	fmt.Printf("Avg drift after break (at → +120s):      %s\n", cents(sumDrift120/n))
	fmt.Printf("\nReversion rate (drift opposite to spike):\n")
	fmt.Printf("  30s:  %s\n", pct(revert30, len(results)))
	fmt.Printf("  60s:  %s\n", pct(revert60, len(results)))
	fmt.Printf("  120s: %s\n", pct(revert120, len(results)))

	// Fade-the-breaker backtest: sell breaker's YES at break, buy back at +60s.
	// PnL = price_at - price_60 (short profit if price drops).
	fmt.Println("\nFade-the-breaker backtest (sell at break, cover at +60s):")
	var pnl float64
	var wins, losses int
	var pnlList []float64
	for _, r := range results {
		if r.PriceAfter60 < 0 {
			continue
		}
		trade := r.PriceAt - r.PriceAfter60 // short: sell high, buy low
		pnl += trade
		pnlList = append(pnlList, trade)
		if trade > 0 {
			wins++
		} else {
			losses++
		}
	}
	if len(pnlList) > 0 {
		fmt.Printf("  Trades: %d, Wins: %d, Losses: %d\n", len(pnlList), wins, losses)
		fmt.Printf("  Hit rate: %s\n", pct(wins, len(pnlList)))
		fmt.Printf("  Total PnL: %s (per $1 stake per trade)\n", cents(pnl))
		fmt.Printf("  Avg PnL/trade: %s\n", cents(pnl/float64(len(pnlList))))
		// Sharpe-like
		mean := pnl / float64(len(pnlList))
		var variance float64
		for _, p := range pnlList {
			variance += (p - mean) * (p - mean)
		}
		std := sqrt(variance / float64(len(pnlList)))
		if std > 0 {
			fmt.Printf("  Sharpe-like (mean/std): %.2f\n", mean/std)
		}
	}

	// Filter: only fades where spike > 5c (big moves = over-reaction candidates)
	fmt.Println("\nFade-the-breaker (only spikes > 5c into break):")
	var bigPnl float64
	var bigWins, bigN int
	var bigPnlList []float64
	for _, r := range results {
		if r.SpikeBefore < 0.05 || r.PriceAfter60 < 0 {
			continue
		}
		trade := r.PriceAt - r.PriceAfter60
		bigPnl += trade
		bigPnlList = append(bigPnlList, trade)
		bigN++
		if trade > 0 {
			bigWins++
		}
	}
	if bigN > 0 {
		fmt.Printf("  Trades: %d, Wins: %d, Hit: %s\n", bigN, bigWins, pct(bigWins, bigN))
		fmt.Printf("  Total PnL: %s, Avg: %s\n", cents(bigPnl), cents(bigPnl/float64(bigN)))
		mean := bigPnl / float64(bigN)
		var variance float64
		for _, p := range bigPnlList {
			variance += (p - mean) * (p - mean)
		}
		std := sqrt(variance / float64(bigN))
		if std > 0 {
			fmt.Printf("  Sharpe-like: %.2f\n", mean/std)
		}
	} else {
		fmt.Println("  No spikes > 5c found.")
	}

	_ = sort.Float64Slice(nil)
}

// priceAt returns the nearest tick price within ±5s of target ts, or -1.
func priceAt(db *sql.DB, marketTicker string, targetTs int64) float64 {
	var price float64
	err := db.QueryRow(`
		SELECT price FROM ticks
		WHERE market_ticker=? AND msg_type='ticker' AND price IS NOT NULL
		  AND ts BETWEEN ? AND ?
		ORDER BY ABS(ts - ?)
		LIMIT 1
	`, marketTicker, targetTs-5000, targetTs+5000, targetTs).Scan(&price)
	if err != nil {
		return -1
	}
	return price
}
