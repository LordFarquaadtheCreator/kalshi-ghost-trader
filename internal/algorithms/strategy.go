// Package algorithms defines pluggable trading strategies for the Kalshi
// tennis markets. Strategies implement the Strategy interface and can be
// dropped into the live WS processor or the backtest engine — one source
// of truth for signal logic.
package algorithms

import (
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// OrderEmitter receives simulated orders from strategies.
// Implemented by TickWriterEmitter (live) and orderCollector (backtest).
type OrderEmitter interface {
	EmitOrder(o store.Order) bool
}

// PriceLookup returns current price info for a market.
// Strategies that track live prices implement this so other consumers
// (e.g. CloseTimer) can query them.
type PriceLookup interface {
	GetPrice(marketTicker string) float64
	GetPriceAge(marketTicker string) time.Duration
}

// Strategy is the interface for trading strategies that can be plugged
// into the live WS processor or the backtest engine.
//
// Lifecycle:
//   - RegisterMarkets is called when a match starts being tracked
//   - OnPrice is called on every WS ticker message (or historical replay)
//   - OnPoints is called when new point-by-point score data arrives
//   - DeletePrice is called when a single market is unsubscribed
//   - UnregisterMarkets is called when a match stops being tracked
type Strategy interface {
	OnPrice(marketTicker string, price float64)
	OnPoints(pts []store.Point)
	RegisterMarkets(eventTicker string, marketTickers []string)
	UnregisterMarkets(eventTicker string)
	DeletePrice(marketTicker string)
}

// TickWriterEmitter adapts store.TickWriter to the OrderEmitter interface.
type TickWriterEmitter struct {
	tw *store.TickWriter
}

// NewTickWriterEmitter wraps a TickWriter as an OrderEmitter.
func NewTickWriterEmitter(tw *store.TickWriter) *TickWriterEmitter {
	return &TickWriterEmitter{tw: tw}
}

func (e *TickWriterEmitter) EmitOrder(o store.Order) bool {
	return e.tw.IngestOrder(o)
}

// OrderCollector collects emitted orders in-memory. Used by backtest.
type OrderCollector struct {
	orders []store.Order
}

// NewOrderCollector creates an in-memory OrderEmitter for backtest.
func NewOrderCollector() *OrderCollector {
	return &OrderCollector{}
}

func (c *OrderCollector) EmitOrder(o store.Order) bool {
	c.orders = append(c.orders, o)
	return true
}

func (c *OrderCollector) Orders() []store.Order {
	return c.orders
}

// NoopEmitter discards all orders. Used when signal generation is disabled.
type NoopEmitter struct{}

func (NoopEmitter) EmitOrder(store.Order) bool { return true }

// NoopStrategy is a no-op Strategy used when no strategy is configured.
type NoopStrategy struct{}

func (NoopStrategy) OnPrice(string, float64)          {}
func (NoopStrategy) OnPoints([]store.Point)           {}
func (NoopStrategy) RegisterMarkets(string, []string) {}
func (NoopStrategy) UnregisterMarkets(string)         {}
func (NoopStrategy) DeletePrice(string)               {}

// Ensure NoopStrategy satisfies Strategy.
var _ Strategy = NoopStrategy{}

// logEmitter wraps an OrderEmitter and logs each emitted order.
type logEmitter struct {
	inner OrderEmitter
	log   *slog.Logger
}

// LogEmitter wraps an OrderEmitter, logging each order before forwarding.
func LogEmitter(inner OrderEmitter, log *slog.Logger) OrderEmitter {
	return &logEmitter{inner: inner, log: log}
}

func (e *logEmitter) EmitOrder(o store.Order) bool {
	ok := e.inner.EmitOrder(o)
	if !ok {
		e.log.Warn("algorithms: order dropped", "match", o.MatchTicker, "market", o.MarketTicker)
	}
	return ok
}
