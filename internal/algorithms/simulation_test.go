package algorithms

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func TestSimulation_HistoricalMatchReplay(t *testing.T) {
	dbPath := findLiveDB(t)
	liveDB := openLiveDB(t, dbPath)
	ctx := context.Background()

	candidate := findSimulationCandidate(t, liveDB, ctx)
	if candidate == nil {
		t.Skip("no match with both ticks and points found")
	}

	t.Logf("candidate: %s (%s vs %s), ticks=%d, points=%d",
		candidate.eventTicker, candidate.homeMkt, candidate.awayMkt,
		candidate.homeTicks+candidate.awayTicks, len(candidate.points))

	e := newTestEnv(t)
	e.strat.RegisterMarkets(candidate.eventTicker, []string{candidate.homeMkt, candidate.awayMkt})

	type replayEvent struct {
		ts     int64
		isTick bool
		tick   store.Tick
		point  store.Point
	}

	var events []replayEvent
	for _, tk := range candidate.homeTicksSlice {
		events = append(events, replayEvent{ts: tk.TS, isTick: true, tick: tk})
	}
	for _, tk := range candidate.awayTicksSlice {
		events = append(events, replayEvent{ts: tk.TS, isTick: true, tick: tk})
	}
	for _, pt := range candidate.points {
		if pt.TsMs > 0 {
			events = append(events, replayEvent{ts: pt.TsMs, point: pt})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })

	type emittedOrder struct {
		point store.Point
		order store.Order
	}
	var emitted []emittedOrder

	for _, ev := range events {
		if ev.isTick {
			if ev.tick.MsgType == "ticker" && ev.tick.Price > 0 {
				e.strat.OnPrice(ev.tick.MarketTicker, ev.tick.Price)
			}
			continue
		}

		e.strat.OnPoints([]store.Point{ev.point})
		orders := e.flushAndQueryOrders(t)
		for _, o := range orders {
			if o.MatchTicker == candidate.eventTicker {
				emitted = append(emitted, emittedOrder{point: ev.point, order: o})
			}
		}
	}

	t.Logf("replay complete: %d events, %d orders emitted", len(events), len(emitted))

	for i, eo := range emitted {
		if eo.order.Action != "buy" {
			t.Errorf("order %d: action=%q, want buy", i, eo.order.Action)
		}
		if eo.order.ConvProb != serveConvProb {
			t.Errorf("order %d: convProb=%v, want %v", i, eo.order.ConvProb, serveConvProb)
		}
		if eo.order.EdgeCents < minEdgeCents {
			t.Errorf("order %d: edgeCents=%d, want >= %d", i, eo.order.EdgeCents, minEdgeCents)
		}
		if eo.order.MarketTicker != candidate.homeMkt && eo.order.MarketTicker != candidate.awayMkt {
			t.Errorf("order %d: marketTicker=%q, not in registered markets", i, eo.order.MarketTicker)
		}
		if eo.order.Context != "home_match_point" && eo.order.Context != "away_match_point" {
			t.Errorf("order %d: context=%q, want home_match_point or away_match_point",
				i, eo.order.Context)
		}

		isServing := (eo.order.Context == "home_match_point" && eo.point.Server == 1) ||
			(eo.order.Context == "away_match_point" && eo.point.Server == 2)
		if !isServing {
			t.Errorf("order %d: fired when MP player not serving (server=%d, context=%q)",
				i, eo.point.Server, eo.order.Context)
		}

		if eo.order.Context == "home_match_point" {
			if !canWinGame(eo.point.HomePoints, eo.point.AwayPoints, eo.point.Server, 1) {
				t.Errorf("order %d: home cannot win game (%s-%s)",
					i, eo.point.HomePoints, eo.point.AwayPoints)
			}
		} else {
			if !canWinGame(eo.point.HomePoints, eo.point.AwayPoints, eo.point.Server, 2) {
				t.Errorf("order %d: away cannot win game (%s-%s)",
					i, eo.point.HomePoints, eo.point.AwayPoints)
			}
		}

		t.Logf("order %d: set=%d game=%d point=%d context=%s edge=%d price=%.2f market=%s",
			i, eo.point.SetNumber, eo.point.GameNumber, eo.point.PointNumber,
			eo.order.Context, eo.order.EdgeCents, eo.order.MarketPrice, eo.order.MarketTicker)
	}

	if len(emitted) == 0 {
		t.Logf("no orders emitted — match may not have reached a serving match point " +
			"or price was too high (edge < 1). Valid but not ideal for testing.")
	}
}

type simCandidate struct {
	eventTicker    string
	homeMkt        string
	awayMkt        string
	homeTicksSlice []store.Tick
	awayTicksSlice []store.Tick
	homeTicks      int
	awayTicks      int
	points         []store.Point
}

func findSimulationCandidate(t *testing.T, db *store.DB, ctx context.Context) *simCandidate {
	t.Helper()

	mkts, err := db.GetSettledMarkets(ctx, 50)
	if err != nil {
		t.Fatalf("GetSettledMarkets: %v", err)
	}

	byEvent := make(map[string][]store.Market)
	for _, m := range mkts {
		byEvent[m.EventTicker] = append(byEvent[m.EventTicker], m)
	}

	for evt, ms := range byEvent {
		if len(ms) < 2 {
			continue
		}

		ptCount, err := db.GetPointCount(ctx, evt)
		if err != nil || ptCount < 20 {
			continue
		}

		pts, err := db.GetPointsByMatch(ctx, evt)
		if err != nil || len(pts) < 20 {
			continue
		}

		homeTicks, err := db.GetTicksByMarket(ctx, ms[0].MarketTicker)
		if err != nil {
			continue
		}
		awayTicks, err := db.GetTicksByMarket(ctx, ms[1].MarketTicker)
		if err != nil {
			continue
		}

		homeTickerCount := 0
		for _, tk := range homeTicks {
			if tk.MsgType == "ticker" && tk.Price > 0 {
				homeTickerCount++
			}
		}
		if homeTickerCount < 5 {
			continue
		}

		return &simCandidate{
			eventTicker:    evt,
			homeMkt:        ms[0].MarketTicker,
			awayMkt:        ms[1].MarketTicker,
			homeTicksSlice: homeTicks,
			awayTicksSlice: awayTicks,
			homeTicks:      len(homeTicks),
			awayTicks:      len(awayTicks),
			points:         pts,
		}
	}

	return nil
}

func TestSimulation_SyntheticFullMatch(t *testing.T) {
	e := newTestEnv(t)
	e.strat.RegisterMarkets("EVT-SIM", []string{"MKT-HOME", "MKT-AWAY"})

	type priceUpdate struct {
		ts    int64
		mkt   string
		price float64
	}
	type pointEvent struct {
		ts int64
		pt store.Point
		mp bool
	}

	baseTS := time.Now().UnixMilli()

	prices := []priceUpdate{
		{baseTS, "MKT-HOME", 0.50},
		{baseTS, "MKT-AWAY", 0.50},
		{baseTS + 60000, "MKT-HOME", 0.55},
		{baseTS + 120000, "MKT-HOME", 0.60},
		{baseTS + 180000, "MKT-HOME", 0.65},
		{baseTS + 240000, "MKT-HOME", 0.70},
		{baseTS + 300000, "MKT-HOME", 0.75},
		{baseTS + 360000, "MKT-HOME", 0.80},
		{baseTS + 420000, "MKT-HOME", 0.85},
	}

	points := []pointEvent{
		{baseTS + 5000, makePoint("EVT-SIM", 1, 1, 1, 1, 0, "15", "0", 0, 0), false},
		{baseTS + 10000, makePoint("EVT-SIM", 1, 10, 4, 1, 1, "40", "30", 6, 4), false},
		{baseTS + 65000, makePoint("EVT-SIM", 2, 1, 1, 1, 0, "15", "0", 0, 0), false},
		{baseTS + 300000, makePoint("EVT-SIM", 2, 9, 1, 1, 0, "40", "30", 4, 4), false},
		{baseTS + 420000, makePoint("EVT-SIM", 2, 10, 1, 1, 0, "40", "30", 5, 4), true},
	}

	type event struct {
		ts       int64
		isPrice  bool
		price    priceUpdate
		isPoint  bool
		pointEvt pointEvent
	}
	var events []event
	for _, p := range prices {
		events = append(events, event{ts: p.ts, isPrice: true, price: p})
	}
	for _, p := range points {
		events = append(events, event{ts: p.ts, isPoint: true, pointEvt: p})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })

	ordersAtMP := 0
	ordersBeforeMP := 0

	for _, ev := range events {
		if ev.isPrice {
			e.strat.OnPrice(ev.price.mkt, ev.price.price)
		}
		if ev.isPoint {
			e.strat.OnPoints([]store.Point{ev.pointEvt.pt})
			orders := e.flushAndQueryOrders(t)
			if len(orders) > 0 {
				if ev.pointEvt.mp {
					ordersAtMP += len(orders)
				} else {
					ordersBeforeMP += len(orders)
				}
			}
		}
	}

	if ordersBeforeMP != 0 {
		t.Fatalf("expected 0 orders before match point, got %d", ordersBeforeMP)
	}
	if ordersAtMP != 1 {
		t.Fatalf("expected 1 order at match point, got %d", ordersAtMP)
	}

	allOrders, err := e.db.GetOrders(context.Background())
	if err != nil {
		t.Fatalf("GetOrders: %v", err)
	}
	if len(allOrders) != 1 {
		t.Fatalf("expected 1 persisted order, got %d", len(allOrders))
	}
	o := allOrders[0]
	if o.Action != "buy" {
		t.Errorf("action=%q, want buy", o.Action)
	}
	if o.MarketTicker != "MKT-HOME" {
		t.Errorf("marketTicker=%q, want MKT-HOME", o.MarketTicker)
	}
	if o.Context != "home_match_point" {
		t.Errorf("context=%q, want home_match_point", o.Context)
	}
	if o.ConvProb != serveConvProb {
		t.Errorf("convProb=%v, want %v", o.ConvProb, serveConvProb)
	}
	if o.EdgeCents != 12 {
		t.Errorf("edgeCents=%d, want 12", o.EdgeCents)
	}
	if o.SetNumber != 2 {
		t.Errorf("setNumber=%d, want 2", o.SetNumber)
	}
}
