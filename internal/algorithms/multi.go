package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// NamedStrategy pairs a strategy with its label for order tagging.
type NamedStrategy struct {
	Name  string
	Strat Strategy
}

// tagEmitter wraps an OrderEmitter and tags each order with a strategy name.
type tagEmitter struct {
	inner    OrderEmitter
	strategy string
}

func (e *tagEmitter) EmitOrder(o store.Order) bool {
	o.Strategy = e.strategy
	return e.inner.EmitOrder(o)
}

// StrategyFactoryFn creates a strategy with the given emitter.
type StrategyFactoryFn func(emitter OrderEmitter) Strategy

// MultiStrategyRuntime wraps multiple strategies, fanning out all Strategy
// calls. Each strategy gets its own tagging emitter that tags orders with
// the strategy name before forwarding to the shared emitter.
// Implements Strategy and ws.PriceUpdater.
type MultiStrategyRuntime struct {
	mu           sync.RWMutex
	strategies   []NamedStrategy
	shared       OrderEmitter
	log          *slog.Logger
	db           *store.DB
	occurrenceTS map[string]int64  // event_ticker → match start time (ms)
	marketEvent  map[string]string // market_ticker → event_ticker
	matchStarted map[string]bool   // event_ticker → true after first OnPoint
}

// NewMultiStrategyFromFactories creates strategies from factory functions.
// Each strategy receives a tagging emitter that stamps orders with the
// strategy name before forwarding to the shared emitter.
func NewMultiStrategyFromFactories(shared OrderEmitter, log *slog.Logger,
	factories map[string]StrategyFactoryFn) *MultiStrategyRuntime {
	runtime := &MultiStrategyRuntime{
		shared:       shared,
		log:          log,
		occurrenceTS: make(map[string]int64),
		marketEvent:  make(map[string]string),
		matchStarted: make(map[string]bool),
	}
	for name, factory := range factories {
		te := &tagEmitter{inner: shared, strategy: name}
		strat := factory(te)
		runtime.strategies = append(runtime.strategies, NamedStrategy{Name: name, Strat: strat})
	}
	return runtime
}

func (m *MultiStrategyRuntime) OnPrice(marketTicker string, price float64) {
	m.mu.RLock()
	eventTicker, hasEvent := m.marketEvent[marketTicker]
	started := true
	if hasEvent {
		started = m.matchStarted[eventTicker]
	}
	m.mu.RUnlock()

	for _, ns := range m.strategies {
		// Gate PreMatchGated strategies until first point received —
		// prevents premature orders from pre-match price movements.
		// Price+time strategies (fadelongshot, nofade) are not gated;
		// their own close_ts window prevents premature firing.
		if _, gated := ns.Strat.(PreMatchGated); gated && hasEvent && !started {
			continue
		}
		ns.Strat.OnPrice(marketTicker, price)
	}
}

func (m *MultiStrategyRuntime) RegisterMarkets(eventTicker string, marketTickers []string) {
	m.mu.Lock()
	for _, mkt := range marketTickers {
		m.marketEvent[mkt] = eventTicker
	}
	if m.db != nil {
		mkts, err := m.db.GetMarketsByEvent(context.Background(), eventTicker)
		if err == nil {
			for _, mk := range mkts {
				if mk.OccurrenceTS > 0 {
					m.occurrenceTS[eventTicker] = mk.OccurrenceTS
					break
				}
			}
		}
	}
	m.mu.Unlock()

	for _, ns := range m.strategies {
		ns.Strat.RegisterMarkets(eventTicker, marketTickers)
	}
}

func (m *MultiStrategyRuntime) UnregisterMarkets(eventTicker string) {
	m.mu.Lock()
	delete(m.occurrenceTS, eventTicker)
	delete(m.matchStarted, eventTicker)
	for mkt, ev := range m.marketEvent {
		if ev == eventTicker {
			delete(m.marketEvent, mkt)
		}
	}
	m.mu.Unlock()

	for _, ns := range m.strategies {
		ns.Strat.UnregisterMarkets(eventTicker)
	}
}

func (m *MultiStrategyRuntime) DeletePrice(marketTicker string) {
	m.mu.Lock()
	delete(m.marketEvent, marketTicker)
	m.mu.Unlock()

	for _, ns := range m.strategies {
		ns.Strat.DeletePrice(marketTicker)
	}
}

// OnPoint fans out score updates to strategies implementing ScoreObserver.
func (m *MultiStrategyRuntime) OnPoint(eventTicker string, p store.Point) {
	m.mu.Lock()
	if !m.matchStarted[eventTicker] {
		m.matchStarted[eventTicker] = true
		m.log.Info("match started (first point received)", "event", eventTicker)
	}
	m.mu.Unlock()

	for _, ns := range m.strategies {
		if obs, ok := ns.Strat.(ScoreObserver); ok {
			obs.OnPoint(eventTicker, p)
		}
	}
}

// SetDB enables schedule refresh support. The DB is used by RefreshOccurrenceTS
// to keep the scheduler's occurrence_ts fresh. Order gating is now handled by
// the first-point guard in OnPrice — orders are blocked until OnPoint fires.
func (m *MultiStrategyRuntime) SetDB(db *store.DB) {
	m.mu.Lock()
	m.db = db
	m.mu.Unlock()
}

// RefreshOccurrenceTS re-reads occurrence_ts from DB for a registered event.
// Called by the schedule checker when Kalshi updates match start times.
func (m *MultiStrategyRuntime) RefreshOccurrenceTS(eventTicker string) {
	if m.db == nil {
		return
	}
	mkts, err := m.db.GetMarketsByEvent(context.Background(), eventTicker)
	if err != nil {
		return
	}
	var occTS int64
	for _, mk := range mkts {
		if mk.OccurrenceTS > 0 {
			occTS = mk.OccurrenceTS
			break
		}
	}
	if occTS == 0 {
		return
	}
	m.mu.Lock()
	if old, has := m.occurrenceTS[eventTicker]; has && old != occTS {
		m.log.Info("occurrence_ts updated",
			"event", eventTicker,
			"old_ts", old,
			"new_ts", occTS)
	}
	m.occurrenceTS[eventTicker] = occTS
	m.mu.Unlock()
}

func (m *MultiStrategyRuntime) String() string {
	names := make([]string, len(m.strategies))
	for i, ns := range m.strategies {
		names[i] = ns.Name
	}
	return fmt.Sprintf("MultiStrategy{%s}", names)
}
