package backtest

import (
	"math"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// resolveOrdersWithSells computes PnL for a mix of buy and sell orders.
// Sells match to prior buys on the same market (FIFO). PnL = (sell_price - buy_price) * size.
// Unmatched buys settle at market close. Unmatched sells skip.
//
// Double-counting avoidance: when a sell matches a buy, the buy's PnL is
// zeroed (the sell carries the round-trip PnL). Both legs are kept in the
// output for display, but only the sell contributes to aggregate PnL.
//
// For buy-only strategies (all existing strategies), this is equivalent to
// per-order settlement logic — no sells means all buys settle at close.
//
// Pure function — no Engine state used. Exported as a method for caller
// ergonomics; the receiver is unused.
func (e *Engine) resolveOrdersWithSells(rawOrders []store.Order, mkts []MarketRow, minPrice float64) []Order {
	return resolveOrdersWithSells(rawOrders, mkts, minPrice)
}

// resolveOrdersWithSells is the standalone implementation. Tests call this
// directly to avoid constructing an Engine.
func resolveOrdersWithSells(rawOrders []store.Order, mkts []MarketRow, minPrice float64) []Order {
	// Build market result lookup.
	resultByMarket := make(map[string]string)
	for _, m := range mkts {
		resultByMarket[m.MarketTicker] = m.Result
	}

	// openBuys tracks unmatched buys per market, FIFO queue.
	// Also tracks the index into orders so we can zero out matched buys.
	type buyEntry struct {
		price    float64
		size     float64
		orderIdx int // index into orders slice
	}
	openBuys := make(map[string][]buyEntry)

	var orders []Order
	for _, o := range rawOrders {
		if minPrice > 0 && o.MarketPrice < minPrice {
			continue
		}
		mktResult := resultByMarket[o.MarketTicker]
		if mktResult == "" {
			continue // unresolved market
		}

		isSell := o.Action == "sell" || o.Side == store.OrderSideClose

		if isSell {
			// Match to oldest unmatched buys on this market (FIFO).
			buys := openBuys[o.MarketTicker]
			if len(buys) == 0 {
				continue // no matching buy — skip (naked short)
			}
			sellSize := o.SuggestedSize
			var sellPnL float64
			for sellSize > 0 && len(buys) > 0 {
				buy := buys[0]
				matchSize := sellSize
				if matchSize > buy.size {
					matchSize = buy.size
				}
				// Round-trip PnL for matched portion.
				sellPnL += (o.MarketPrice - buy.price) * matchSize
				buy.size -= matchSize
				sellSize -= matchSize

				// Zero out the matched buy's PnL — sell carries the round-trip.
				orders[buy.orderIdx].PnL = 0
				orders[buy.orderIdx].Won = false
				orders[buy.orderIdx].Context += " [matched]"

				if buy.size <= 0 {
					buys = buys[1:]
				} else {
					buys[0] = buy
				}
			}
			openBuys[o.MarketTicker] = buys

			won := sellPnL > 0
			orders = append(orders, Order{
				Match:     o.MatchTicker,
				Market:    o.MarketTicker,
				Context:   o.Context,
				SetNum:    o.SetNumber,
				Price:     o.MarketPrice,
				EdgeCents: o.EdgeCents,
				Size:      o.SuggestedSize,
				Won:       won,
				PnL:       sellPnL,
				Result:    mktResult,
				TS:        o.TS,
				Side:      "close",
			})
		} else {
			// Buy — queue for matching with future sells.
			idx := len(orders)
			openBuys[o.MarketTicker] = append(openBuys[o.MarketTicker], buyEntry{
				price:    o.MarketPrice,
				size:     o.SuggestedSize,
				orderIdx: idx,
			})

			// Default settlement PnL (used if no sell matches — hold to close).
			won := mktResult == "yes"
			if o.Action == "buy_no" {
				won = mktResult == "no"
			}
			var pnl float64
			if won {
				pnl = o.SuggestedSize * (1.0 - o.MarketPrice)
			} else {
				pnl = -o.SuggestedSize * o.MarketPrice
			}
			orders = append(orders, Order{
				Match:     o.MatchTicker,
				Market:    o.MarketTicker,
				Context:   o.Context,
				SetNum:    o.SetNumber,
				Price:     o.MarketPrice,
				EdgeCents: o.EdgeCents,
				Size:      o.SuggestedSize,
				Won:       won,
				PnL:       pnl,
				Result:    mktResult,
				TS:        o.TS,
				Side:      "open",
			})
		}
	}

	return orders
}

// computeSummary aggregates per-strategy stats from resolved orders.
func computeSummary(orders []Order) Summary {
	s := Summary{TotalSignals: len(orders)}
	if len(orders) == 0 {
		return s
	}

	for _, o := range orders {
		if o.Won {
			s.Wins++
			s.TotalPayout += o.Size
		} else {
			s.Losses++
		}
		s.NetPnL += o.PnL
		s.TotalInvested += o.Size * o.Price
		s.AvgEdge += float64(o.EdgeCents)
		s.AvgSize += o.Size
		s.AvgPrice += o.Price
	}

	n := float64(len(orders))
	s.WinRate = float64(s.Wins) / n * 100
	if s.TotalInvested > 0 {
		s.ROI = s.NetPnL / s.TotalInvested * 100
	}
	s.AvgEdge /= n
	s.AvgSize /= n
	s.AvgPrice /= n

	// Risk-adjusted metrics
	var sumSqDev, sumDownside, grossWin, grossLoss float64
	var cumulative, peak, maxDD float64
	for _, o := range orders {
		dev := o.PnL - (s.NetPnL / n)
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
	s.StdDev = sqrt(sumSqDev / n)
	s.DownsideDev = sqrt(sumDownside / n)
	s.MaxDrawdown = maxDD
	if s.StdDev > 0 {
		s.Sharpe = (s.NetPnL / n) / s.StdDev
	}
	if s.DownsideDev > 0 {
		s.Sortino = (s.NetPnL / n) / s.DownsideDev
	}
	if grossLoss > 0 {
		s.ProfitFactor = grossWin / grossLoss
	}

	return s
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Sqrt(x)
}
