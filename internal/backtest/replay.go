package backtest

import (
	"sort"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// replayEvent kinds.
const (
	eventPrice = 0
	eventScore = 1
)

// replayEvent is a single timestamped event in the merged replay stream.
// kind=eventPrice → price tick on mkt at price.
// kind=eventScore → point-by-point score event in point.
type replayEvent struct {
	ts    int64
	kind  int
	mkt   string
	price float64
	point store.Point
}

// buildEvents merges price ticks (home + away markets) with point-by-point
// score events for a match, sorted by timestamp. Called once per match and
// cached in Engine.eventsByMatch.
func buildEvents(
	tickPrices map[string][]TickPrice,
	points map[string][]store.Point,
	matchTicker, homeMkt, awayMkt string,
) []replayEvent {
	var events []replayEvent

	for _, mkt := range []string{homeMkt, awayMkt} {
		for _, t := range tickPrices[mkt] {
			events = append(events, replayEvent{ts: t.TS, kind: eventPrice, mkt: mkt, price: t.Price})
		}
	}
	for _, p := range points[matchTicker] {
		events = append(events, replayEvent{ts: p.TS, kind: eventScore, point: p})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })
	return events
}

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

// replayInterleaved feeds cached merged events to a single strategy.
// Used by RunStrategy (single-strategy path). Score events only fed to
// strategies implementing ScoreObserver.
func (e *Engine) replayInterleaved(strat ReplayStrategy, matchTicker, homeMkt, awayMkt string) {
	events := e.cachedEvents(matchTicker, homeMkt, awayMkt)
	scoreObs, _ := strat.(algorithms.ScoreObserver)

	for _, ev := range events {
		ts := time.UnixMilli(ev.ts)
		strat.SetReplayTime(ts)
		if ev.kind == eventPrice {
			strat.OnPriceAt(ev.mkt, ev.price, ts)
		} else if scoreObs != nil {
			scoreObs.OnPoint(matchTicker, ev.point)
		}
	}
}

// runCloseTimeBacktest replays tick data through a single close-time
// strategy. Used by RunStrategy (single-strategy path).
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

		homeMkt, awayMkt := e.cachedMarketOrder(matchTicker, mkts)
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

// runCloseTimeBacktestBroadcast runs all close-time strategies in a single
// pass over matches. Each strategy gets its own collector. Returns per-strategy
// orders + per-strategy match counts.
func (e *Engine) runCloseTimeBacktestBroadcast(factories map[string]StrategyFactory, minPrice float64) (map[string][]Order, map[string]int) {
	ordersByName := make(map[string][]Order, len(factories))
	bothByName := make(map[string]int, len(factories))
	if len(factories) == 0 {
		return ordersByName, bothByName
	}

	type stratState struct {
		name      string
		cts       CloseTimeStrategy
		strat     ReplayStrategy
		collector *algorithms.OrderCollector
	}

	// One strategy instance per factory — reused across all matches in close-time
	// path (matches original runCloseTimeBacktest semantics: single strat instance
	// registered/unregistered per match).
	states := make([]stratState, 0, len(factories))
	for name, factory := range factories {
		c := algorithms.NewOrderCollector()
		s := factory(c, e.log)
		cts, ok := s.(CloseTimeStrategy)
		if !ok {
			continue
		}
		states = append(states, stratState{name: name, cts: cts, strat: s, collector: c})
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

		homeMkt, awayMkt := e.cachedMarketOrder(matchTicker, mkts)

		for i := range states {
			states[i].cts.RegisterCloseTime(matchTicker, closeTs)
			states[i].strat.RegisterMarkets(matchTicker, []string{homeMkt, awayMkt})
			e.wireStrategyContext(states[i].strat, matchTicker, homeMkt, awayMkt)
		}

		for _, mkt := range []string{homeMkt, awayMkt} {
			ticks := e.tickPrices[mkt]
			for _, t := range ticks {
				ts := time.UnixMilli(t.TS)
				for i := range states {
					states[i].strat.OnPriceAt(mkt, t.Price, ts)
				}
			}
		}

		for i := range states {
			states[i].strat.UnregisterMarkets(matchTicker)
			bothByName[states[i].name]++
		}
	}

	// Collect all market rows for sell-aware PnL processing.
	var allMkts []MarketRow
	for _, mkts := range e.markets {
		allMkts = append(allMkts, mkts...)
	}
	for i := range states {
		resolved := e.resolveOrdersWithSells(states[i].collector.Orders(), allMkts, minPrice)
		ordersByName[states[i].name] = append(ordersByName[states[i].name], resolved...)
	}
	return ordersByName, bothByName
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
