package backtest

import (
	"sort"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// wireStrategyContext sets series, surface, and volume data on strategies
// that implement the corresponding setter interfaces.
func (e *Engine) wireStrategyContext(strat ReplayStrategy, matchTicker, homeMkt, awayMkt string) {
	if ss, ok := strat.(SeriesSetter); ok {
		if series := e.eventSeries[matchTicker]; series != "" {
			ss.SetSeriesTicker(matchTicker, series)
		}
	}
	if ss, ok := strat.(SurfaceSetter); ok {
		if surface := e.eventSurface[matchTicker]; surface != "" {
			ss.SetSurface(matchTicker, surface)
		}
	}
	if vs, ok := strat.(VolumeSetter); ok {
		if vols := e.tickVolumes[homeMkt]; len(vols) > 0 {
			vs.SetVolumeSeries(homeMkt, toAlgoVolumes(vols))
		}
		if vols := e.tickVolumes[awayMkt]; len(vols) > 0 {
			vs.SetVolumeSeries(awayMkt, toAlgoVolumes(vols))
		}
	}
}

// toAlgoVolumes converts engine TickVolume slice to algorithms.TickVolume slice.
func toAlgoVolumes(vols []TickVolume) []algorithms.TickVolume {
	out := make([]algorithms.TickVolume, len(vols))
	for i, v := range vols {
		out[i] = algorithms.TickVolume{TS: v.TS, DollarVolume: v.DollarVolume}
	}
	return out
}

// replayInterleaved feeds price ticks and score events to a strategy in
// timestamp order. Price ticks from both markets are merged with point-by-point
// score data, then replayed chronologically. Score events are only fed to
// strategies implementing ScoreObserver.
func (e *Engine) replayInterleaved(strat ReplayStrategy, matchTicker, homeMkt, awayMkt string) {
	type event struct {
		ts    int64
		kind  int // 0=price, 1=score
		mkt   string
		price float64
		point store.Point
	}

	var events []event

	for _, mkt := range []string{homeMkt, awayMkt} {
		for _, t := range e.tickPrices[mkt] {
			events = append(events, event{ts: t.TS, kind: 0, mkt: mkt, price: t.Price})
		}
	}

	for _, p := range e.points[matchTicker] {
		events = append(events, event{ts: p.TS, kind: 1, point: p})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })

	scoreObs, _ := strat.(algorithms.ScoreObserver)

	for _, ev := range events {
		ts := time.UnixMilli(ev.ts)
		strat.SetReplayTime(ts)
		if ev.kind == 0 {
			strat.OnPriceAt(ev.mkt, ev.price, ts)
		} else if scoreObs != nil {
			scoreObs.OnPoint(matchTicker, ev.point)
		}
	}
}

// runCloseTimeBacktest replays tick data through strategies that use
// close_ts (e.g. fadelongshot). Iterates ALL finalized events with both
// markets and close_ts, not just those with points data.
func (e *Engine) runCloseTimeBacktest(factory StrategyFactory, minPrice float64) []Order {
	collector := algorithms.NewOrderCollector()
	strat := factory(collector, e.log)

	cts, ok := strat.(CloseTimeStrategy)
	if !ok {
		return nil
	}

	for matchTicker, mkts := range e.markets {
		closeTs, ok := e.marketCloseTs[matchTicker]
		if !ok || closeTs == 0 {
			continue
		}
		if len(mkts) < 2 {
			continue
		}
		finalized := false
		for _, m := range mkts {
			if m.Status == "finalized" {
				finalized = true
				break
			}
		}
		if !finalized {
			continue
		}

		homeMkt, awayMkt := e.orderMarketsByTitle(matchTicker, mkts)
		cts.RegisterCloseTime(matchTicker, closeTs)
		strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})
		e.wireStrategyContext(strat, matchTicker, homeMkt, awayMkt)

		for _, mkt := range []string{homeMkt, awayMkt} {
			ticks := e.tickPrices[mkt]
			for _, t := range ticks {
				strat.OnPriceAt(mkt, t.Price, time.UnixMilli(t.TS))
			}
		}
		strat.UnregisterMarkets(matchTicker)
	}

	// Collect all market rows for sell-aware PnL processing.
	var allMkts []MarketRow
	for _, mkts := range e.markets {
		allMkts = append(allMkts, mkts...)
	}
	return e.resolveOrdersWithSells(collector.Orders(), allMkts, minPrice)
}

// orderMarketsByTitle determines [home, away] market order from the event title.
// Kalshi titles are "Home vs Away". Falls back to DB order if matching fails.
func (e *Engine) orderMarketsByTitle(eventTicker string, mkts []MarketRow) (home, away string) {
	if len(mkts) < 2 {
		return mkts[0].MarketTicker, ""
	}
	title, ok := e.eventTitles[eventTicker]
	if !ok {
		return mkts[0].MarketTicker, mkts[1].MarketTicker
	}
	parts := strings.SplitN(title, " vs ", 2)
	if len(parts) != 2 {
		return mkts[0].MarketTicker, mkts[1].MarketTicker
	}
	homeLN := lastName(strings.TrimSpace(parts[0]))
	for _, m := range mkts {
		if lastName(m.PlayerName) == homeLN {
			return m.MarketTicker, otherMarket(mkts, m.MarketTicker)
		}
	}
	return mkts[0].MarketTicker, mkts[1].MarketTicker
}

func lastName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parts[len(parts)-1], "."))
}

func otherMarket(mkts []MarketRow, skip string) string {
	for _, m := range mkts {
		if m.MarketTicker != skip {
			return m.MarketTicker
		}
	}
	return ""
}
