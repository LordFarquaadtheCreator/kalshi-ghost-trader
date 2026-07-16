// Command backtest replays historical point + tick data from the SQLite DB
// through the match-point signal strategy and reports P&L.
//
// Usage:
//
//	go run ./cmd/backtest [db_path]
//
// Default db_path is kalshi_tennis.db in the current directory.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	setsToWin     = 2
	gamesPerSet   = 6
	serveConvProb = 0.97
	minEdgeCents  = 1
	baseSize      = 10.0
	maxSize       = 100.0
	priceStaleTTL = 60 * time.Second
)

type pointRow struct {
	ts         int64
	setNum     int
	gameNum    int
	pointNum   int
	server     int
	scorer     int
	homePts    string
	awayPts    string
	homeGames  int
	awayGames  int
	isTiebreak bool
}

type marketRow struct {
	marketTicker string
	playerName   string
	result       string
	status       string
}

type tickPrice struct {
	ts    int64
	price float64
}

type order struct {
	match     string
	market    string
	context   string
	setNum    int
	gameNum   int
	pointNum  int
	server    int
	scorer    int
	homeGames int
	awayGames int
	homePts   string
	awayPts   string
	price     float64
	edgeCents int
	size      float64
	won       bool
	pnl       float64
	result    string
}

func main() {
	dbPath := flag.String("db", "kalshi_tennis.db", "path to SQLite DB")
	minPrice := flag.Float64("min-price", 0.0, "skip signals below this market price (0=disabled)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx := context.Background()

	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)", *dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Load markets
	fmt.Println("Loading markets...")
	markets := make(map[string][]marketRow)
	marketRows, err := db.QueryContext(ctx, `SELECT event_ticker, market_ticker, player_name, result, status FROM markets ORDER BY event_ticker, market_ticker`)
	if err != nil {
		log.Error("query markets", "err", err)
		os.Exit(1)
	}
	for marketRows.Next() {
		var et, mt, pn, res, st string
		if err := marketRows.Scan(&et, &mt, &pn, &res, &st); err != nil {
			log.Error("scan market", "err", err)
			os.Exit(1)
		}
		markets[et] = append(markets[et], marketRow{mt, pn, res, st})
	}
	marketRows.Close()

	// Load tick prices per market
	fmt.Println("Loading tick prices...")
	tickPrices := make(map[string][]tickPrice)
	tickRows, err := db.QueryContext(ctx, `SELECT market_ticker, ts, price FROM ticks WHERE price IS NOT NULL AND price > 0 ORDER BY market_ticker, ts`)
	if err != nil {
		log.Error("query ticks", "err", err)
		os.Exit(1)
	}
	count := 0
	for tickRows.Next() {
		var mt string
		var ts int64
		var price float64
		if err := tickRows.Scan(&mt, &ts, &price); err != nil {
			log.Error("scan tick", "err", err)
			os.Exit(1)
		}
		tickPrices[mt] = append(tickPrices[mt], tickPrice{ts, price})
		count++
	}
	tickRows.Close()
	fmt.Printf("Loaded %d tick prices across %d markets\n", count, len(tickPrices))

	// Load points
	fmt.Println("Loading points...")
	pointsByMatch := make(map[string][]pointRow)
	pointRows, err := db.QueryContext(ctx, `
		SELECT match_ticker, ts_ms, set_number, game_number, point_number,
		       server, scorer, home_points, away_points, home_games, away_games,
		       is_tiebreak
		FROM points WHERE ts_ms IS NOT NULL
		ORDER BY match_ticker, ts_ms`)
	if err != nil {
		log.Error("query points", "err", err)
		os.Exit(1)
	}
	for pointRows.Next() {
		var mt string
		var ts int64
		var setNum, gameNum, pointNum, server, scorer, homeGames, awayGames int
		var homePts, awayPts string
		var isTB int
		if err := pointRows.Scan(&mt, &ts, &setNum, &gameNum, &pointNum, &server, &scorer, &homePts, &awayPts, &homeGames, &awayGames, &isTB); err != nil {
			log.Error("scan point", "err", err)
			os.Exit(1)
		}
		pointsByMatch[mt] = append(pointsByMatch[mt], pointRow{
			ts: ts, setNum: setNum, gameNum: gameNum, pointNum: pointNum,
			server: server, scorer: scorer, homePts: homePts, awayPts: awayPts,
			homeGames: homeGames, awayGames: awayGames, isTiebreak: isTB == 1,
		})
	}
	pointRows.Close()

	fmt.Printf("Matches with points: %d\n", len(pointsByMatch))
	fmt.Printf("Matches with markets: %d\n", len(markets))

	// Backtest
	var orders []order
	both := 0
	for matchTicker, pts := range pointsByMatch {
		mkts, ok := markets[matchTicker]
		if !ok || len(mkts) < 2 {
			continue
		}
		both++

		homeMkt := mkts[0].marketTicker
		awayMkt := mkts[1].marketTicker

		setsHome, setsAway := 0, 0
		lastSet, lastHomeGames, lastAwayGames, lastScorer := 0, 0, 0, 0
		seen := make(map[string]bool)

		for _, p := range pts {
			key := fmt.Sprintf("%d:%d:%d", p.setNum, p.gameNum, p.pointNum)
			if seen[key] {
				continue
			}
			seen[key] = true

			if p.setNum > lastSet && lastSet > 0 {
				if lastHomeGames > lastAwayGames {
					setsHome++
				} else if lastAwayGames > lastHomeGames {
					setsAway++
				} else if lastScorer != 0 {
					if lastScorer == 1 {
						setsHome++
					} else {
						setsAway++
					}
				}
			}
			lastSet = p.setNum
			lastHomeGames = p.homeGames
			lastAwayGames = p.awayGames
			lastScorer = p.scorer

			homeNeedsSet := setsToWin - setsHome
			awayNeedsSet := setsToWin - setsAway
			if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
				continue
			}

			if p.isTiebreak {
				continue
			}

			homeCanWin := canWinGame(p.homePts, p.awayPts, p.server, 1)
			awayCanWin := canWinGame(p.homePts, p.awayPts, p.server, 2)

			homeMP := homeNeedsSet == 1 && homeCanWin && p.homeGames >= gamesPerSet-1 && p.homeGames > p.awayGames
			awayMP := awayNeedsSet == 1 && awayCanWin && p.awayGames >= gamesPerSet-1 && p.awayGames > p.homeGames

			if !homeMP && !awayMP {
				continue
			}

			winner := 2
			ctxStr := "away_match_point"
			mktTicker := awayMkt
			if homeMP {
				winner = 1
				ctxStr = "home_match_point"
				mktTicker = homeMkt
			}

			isServing := (winner == 1 && p.server == 1) || (winner == 2 && p.server == 2)
			if !isServing {
				continue
			}

			price := getPriceAt(tickPrices, mktTicker, p.ts)
			if price <= 0 {
				continue
			}
			if price < *minPrice {
				continue
			}

			edgeCents := int((serveConvProb-price)*100 + 1e-9)
			if edgeCents < minEdgeCents {
				continue
			}

			size := suggestedSize(edgeCents)

			mktResult := ""
			for _, m := range mkts {
				if m.marketTicker == mktTicker {
					mktResult = m.result
					break
				}
			}
			won := mktResult == "yes"
			var pnl float64
			if won {
				pnl = size * (1.0 - price)
			} else {
				pnl = -size * price
			}

			orders = append(orders, order{
				match: matchTicker, market: mktTicker, context: ctxStr,
				setNum: p.setNum, gameNum: p.gameNum, pointNum: p.pointNum,
				server: p.server, scorer: p.scorer,
				homeGames: p.homeGames, awayGames: p.awayGames,
				homePts: p.homePts, awayPts: p.awayPts,
				price: price, edgeCents: edgeCents, size: size,
				won: won, pnl: pnl, result: mktResult,
			})
		}
	}

	// Summary
	fmt.Printf("\n%s\n", strings.Repeat("=", 80))
	fmt.Println("BACKTEST RESULTS")
	fmt.Printf("%s\n", strings.Repeat("=", 80))
	fmt.Printf("Matches with both points and markets: %d\n", both)
	fmt.Printf("Total signals: %d\n", len(orders))

	if len(orders) == 0 {
		fmt.Println("No orders would have been emitted.")
		return
	}

	wins, losses := 0, 0
	var totalPnL, totalInvested, totalPayout float64
	for _, o := range orders {
		if o.won {
			wins++
			totalPayout += o.size
		} else {
			losses++
		}
		totalPnL += o.pnl
		totalInvested += o.size * o.price
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
		sumEdge += float64(o.edgeCents)
		sumSize += o.size
		sumPrice += o.price
	}
	n := float64(len(orders))
	fmt.Printf("Avg edge: %.1f cents\n", sumEdge/n)
	fmt.Printf("Avg size: %.1f\n", sumSize/n)
	fmt.Printf("Avg price: %.3f\n", sumPrice/n)

	// Per-order detail
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].match != orders[j].match {
			return orders[i].match < orders[j].match
		}
		return orders[i].pointNum < orders[j].pointNum
	})

	fmt.Printf("\n%-45s %-20s %6s %5s %6s %4s %8s\n", "match", "ctx", "price", "edge", "size", "won", "pnl")
	fmt.Println(strings.Repeat("-", 100))
	for _, o := range orders {
		wonStr := "N"
		if o.won {
			wonStr = "Y"
		}
		fmt.Printf("%-45s %-20s %6.3f %5dc %6.1f %4s %8.2f\n",
			o.match, o.context, o.price, o.edgeCents, o.size, wonStr, o.pnl)
	}
}

func canWinGame(homePts, awayPts string, server, player int) bool {
	h := normalizeScore(homePts)
	a := normalizeScore(awayPts)
	if player == 1 {
		return h == "A" || (h == "40" && a != "40" && a != "A")
	}
	return a == "A" || (a == "40" && h != "40" && h != "A")
}

func normalizeScore(s string) string {
	switch s {
	case "0", "15", "30", "40", "A":
		return s
	default:
		return ""
	}
}

func suggestedSize(edgeCents int) float64 {
	size := baseSize * float64(edgeCents) / float64(minEdgeCents)
	if size > maxSize {
		size = maxSize
	}
	return size
}

func getPriceAt(prices map[string][]tickPrice, marketTicker string, ts int64) float64 {
	ticks := prices[marketTicker]
	if len(ticks) == 0 {
		return 0
	}
	lo, hi := 0, len(ticks)-1
	result := 0.0
	for lo <= hi {
		mid := (lo + hi) / 2
		if ticks[mid].ts <= ts {
			result = ticks[mid].price
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return result
}
