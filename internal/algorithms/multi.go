package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	if eventTicker, ok := m.marketEvent[marketTicker]; ok {
		if occTS, has := m.occurrenceTS[eventTicker]; has && time.Now().UnixMilli() < occTS {
			m.mu.RUnlock()
			return
		}
	}
	m.mu.RUnlock()

	for _, ns := range m.strategies {
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
	for _, ns := range m.strategies {
		if obs, ok := ns.Strat.(ScoreObserver); ok {
			obs.OnPoint(eventTicker, p)
		}
	}
}

// SetDB enables pre-match order gating. When set, OnPrice calls are dropped
// for markets whose occurrence_ts hasn't been reached yet.
func (m *MultiStrategyRuntime) SetDB(db *store.DB) {
	m.mu.Lock()
	m.db = db
	m.mu.Unlock()
}

func (m *MultiStrategyRuntime) String() string {
	names := make([]string, len(m.strategies))
	for i, ns := range m.strategies {
		names[i] = ns.Name
	}
	return fmt.Sprintf("MultiStrategy{%s}", names)
}
