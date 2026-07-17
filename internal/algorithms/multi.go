package algorithms

import (
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
	mu         sync.RWMutex
	strategies []NamedStrategy
	shared     OrderEmitter
	log        *slog.Logger
}

// NewMultiStrategyFromFactories creates strategies from factory functions.
// Each strategy receives a tagging emitter that stamps orders with the
// strategy name before forwarding to the shared emitter.
func NewMultiStrategyFromFactories(shared OrderEmitter, log *slog.Logger,
	factories map[string]StrategyFactoryFn) *MultiStrategyRuntime {
	runtime := &MultiStrategyRuntime{
		shared: shared,
		log:    log,
	}
	for name, factory := range factories {
		te := &tagEmitter{inner: shared, strategy: name}
		strat := factory(te)
		runtime.strategies = append(runtime.strategies, NamedStrategy{Name: name, Strat: strat})
	}
	return runtime
}

func (m *MultiStrategyRuntime) OnPrice(marketTicker string, price float64) {
	for _, ns := range m.strategies {
		ns.Strat.OnPrice(marketTicker, price)
	}
}

func (m *MultiStrategyRuntime) RegisterMarkets(eventTicker string, marketTickers []string) {
	for _, ns := range m.strategies {
		ns.Strat.RegisterMarkets(eventTicker, marketTickers)
	}
}

func (m *MultiStrategyRuntime) UnregisterMarkets(eventTicker string) {
	for _, ns := range m.strategies {
		ns.Strat.UnregisterMarkets(eventTicker)
	}
}

func (m *MultiStrategyRuntime) DeletePrice(marketTicker string) {
	for _, ns := range m.strategies {
		ns.Strat.DeletePrice(marketTicker)
	}
}

func (m *MultiStrategyRuntime) String() string {
	names := make([]string, len(m.strategies))
	for i, ns := range m.strategies {
		names[i] = ns.Name
	}
	return fmt.Sprintf("MultiStrategy{%s}", names)
}
