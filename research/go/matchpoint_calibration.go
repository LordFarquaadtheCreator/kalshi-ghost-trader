package main

import (
	"database/sql"
	"fmt"
	"os"
	"sort"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
)

// MatchpointCalibration tests RQ8: at each match point, market price implies
// a conversion prob. Empirical conversion rate? Calibration curve.
// Recomputes is_match_point from score state (DB column unreliable).
// Tracks sets won by detecting set transitions (home_set_games is NULL in data).
func init() { register(&matchpointCalibrationModule{}) }

type matchpointCalibrationModule struct{}

func (m matchpointCalibrationModule) Name() string { return "mp-calibration" }
func (m matchpointCalibrationModule) Desc() string { return "RQ8: match-point market price vs empirical conversion calibration" }

func (m matchpointCalibrationModule) Run(db *sql.DB, args []string) {
	fmt.Println("RQ8: Match-point calibration")
	fmt.Println("============================")

	// Load all points with timestamps, ordered by match + sequence
	rows, err := db.Query(`
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points,
		       home_games, away_games, is_tiebreak
		FROM points
		WHERE ts_ms IS NOT NULL
		ORDER BY match_ticker, set_number, game_number, point_number
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query points: %v\n", err)
		return
	}
	defer rows.Close()

	type point struct {
		MatchTicker string
		TsMs        int64
		SetNum      int
		GameNum     int
		PointNum    int
		Server      int
		Scorer      int
		HomePoints  string
		AwayPoints  string
		HomeGames   int
		AwayGames   int
		IsTiebreak  bool
	}

	matchPoints := map[string][]point{}
	for rows.Next() {
		var p point
		var isTB int
		rows.Scan(&p.MatchTicker, &p.TsMs, &p.SetNum, &p.GameNum, &p.PointNum,
			&p.Server, &p.Scorer, &p.HomePoints, &p.AwayPoints,
			&p.HomeGames, &p.AwayGames, &isTB)
		p.IsTiebreak = isTB != 0
		matchPoints[p.MatchTicker] = append(matchPoints[p.MatchTicker], p)
	}
	fmt.Printf("Loaded points for %d matches\n", len(matchPoints))

	// Cache market tickers per event
	marketCache := map[string][]string{}
	loadMarkets := func(ticker string) []string {
		if m, ok := marketCache[ticker]; ok {
			return m
		}
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
		marketCache[ticker] = mts
		return mts
	}

	// Cache market results
	resultCache := map[string]string{}
	loadResult := func(marketTicker string) string {
		if r, ok := resultCache[marketTicker]; ok {
			return r
		}
		var r string
		db.QueryRow(`SELECT result FROM markets WHERE market_ticker=?`, marketTicker).Scan(&r)
		resultCache[marketTicker] = r
		return r
	}

	var mpEvents []matchPointEvent
	for ticker, pts := range matchPoints {
		// Track sets won by detecting set transitions
		setsHome := 0
		setsAway := 0
		prevSetNum := 0
		var prevHomeGames, prevAwayGames int

		for _, p := range pts {
			// Set transition: set number changed → previous set ended
			if prevSetNum != 0 && p.SetNum != prevSetNum {
				if prevHomeGames > prevAwayGames {
					setsHome++
				} else if prevAwayGames > prevHomeGames {
					setsAway++
				}
			}
			prevSetNum = p.SetNum
			prevHomeGames = p.HomeGames
			prevAwayGames = p.AwayGames

			// Classify point
			ctx := algorithms.PointContext{
				SetsHome:   setsHome,
				SetsAway:   setsAway,
				HomeGames:  p.HomeGames,
				AwayGames:  p.AwayGames,
				HomePoints: p.HomePoints,
				AwayPoints: p.AwayPoints,
				Server:     p.Server,
				IsTiebreak: p.IsTiebreak,
			}
			class := algorithms.ClassifyPoint(ctx)
			if !class.IsMatchPoint {
				continue
			}

			// Determine MP player
			homeCanWinSet := p.HomeGames >= 5 && p.HomeGames > p.AwayGames
			awayCanWinSet := p.AwayGames >= 5 && p.AwayGames > p.HomeGames

			var mpPlayer int
			if homeCanWinSet && setsHome == 1 {
				mpPlayer = 1
			} else if awayCanWinSet && setsAway == 1 {
				mpPlayer = 2
			} else {
				continue
			}

			mts := loadMarkets(ticker)
			if len(mts) < 2 {
				continue
			}
			mpMkt := mts[0]
			if mpPlayer == 2 {
				mpMkt = mts[1]
			}

			price := priceAt(db, mpMkt, p.TsMs)
			if price < 0 {
				continue
			}

			mpEvents = append(mpEvents, matchPointEvent{
				MatchTicker: ticker,
				TsMs:        p.TsMs,
				Serving:     p.Server == mpPlayer,
				MarketPrice: price,
				MarketMkt:   mpMkt,
			})
		}
	}

	fmt.Printf("\nMatch-point events with price data: %d\n", len(mpEvents))
	if len(mpEvents) == 0 {
		fmt.Println("No match points with price coverage. Need more gold-set data.")
		return
	}

	// Determine conversion from market result
	conv := 0
	for _, e := range mpEvents {
		if loadResult(e.MarketMkt) == "yes" {
			conv++
		}
	}

	fmt.Printf("Overall conversion: %s (%d/%d)\n", pct(conv, len(mpEvents)), conv, len(mpEvents))
	fmt.Printf("Avg market price at MP: %s\n", cents(avgMP(mpEvents)))

	// Serving vs returning
	var serving, returning []matchPointEvent
	for _, e := range mpEvents {
		if e.Serving {
			serving = append(serving, e)
		} else {
			returning = append(returning, e)
		}
	}
	sConv := 0
	for _, e := range serving {
		if loadResult(e.MarketMkt) == "yes" {
			sConv++
		}
	}
	rConv := 0
	for _, e := range returning {
		if loadResult(e.MarketMkt) == "yes" {
			rConv++
		}
	}
	fmt.Println("\nServing for match:")
	fmt.Printf("  n=%d, conversion=%s, avg price=%s\n", len(serving), pct(sConv, len(serving)), cents(avgMP(serving)))
	fmt.Println("Returning for match (breaking):")
	fmt.Printf("  n=%d, conversion=%s, avg price=%s\n", len(returning), pct(rConv, len(returning)), cents(avgMP(returning)))

	// Edge: empirical - market price
	if len(serving) > 0 {
		sEmp := float64(sConv) / float64(len(serving))
		sAvg := avgMP(serving)
		fmt.Printf("  Serving edge: %s (empirical %s vs market %s)\n", cents(sEmp-sAvg), pctF(sEmp), cents(sAvg))
	}
	if len(returning) > 0 {
		rEmp := float64(rConv) / float64(len(returning))
		rAvg := avgMP(returning)
		fmt.Printf("  Returning edge: %s (empirical %s vs market %s)\n", cents(rEmp-rAvg), pctF(rEmp), cents(rAvg))
	}

	// Calibration by price quintile
	sort.Slice(mpEvents, func(i, j int) bool {
		return mpEvents[i].MarketPrice < mpEvents[j].MarketPrice
	})
	fmt.Println("\nCalibration by price quintile:")
	fmt.Printf("  %-14s %-6s %-12s %-12s %s\n", "price_range", "n", "avg_price", "empirical", "edge")
	bucketSize := len(mpEvents) / 5
	if bucketSize == 0 {
		bucketSize = 1
	}
	for b := 0; b < 5; b++ {
		start := b * bucketSize
		end := start + bucketSize
		if b == 4 {
			end = len(mpEvents)
		}
		if start >= len(mpEvents) {
			break
		}
		bucket := mpEvents[start:end]
		bConv := 0
		var sumP float64
		for _, e := range bucket {
			if loadResult(e.MarketMkt) == "yes" {
				bConv++
			}
			sumP += e.MarketPrice
		}
		emp := float64(bConv) / float64(len(bucket))
		avgP := sumP / float64(len(bucket))
		edge := emp - avgP
		fmt.Printf("  %.2f-%.2f      %-6d %-12s %-12s %s\n",
			bucket[0].MarketPrice, bucket[len(bucket)-1].MarketPrice,
			len(bucket), cents(avgP), pctF(emp), cents(edge))
	}

	_ = os.Stdout
}

type matchPointEvent struct {
	MatchTicker string
	TsMs        int64
	Serving     bool
	MarketPrice float64
	MarketMkt   string
}

func avgMP(events []matchPointEvent) float64 {
	if len(events) == 0 {
		return 0
	}
	s := 0.0
	for _, e := range events {
		s += e.MarketPrice
	}
	return s / float64(len(events))
}
