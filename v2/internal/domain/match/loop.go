package match

import (
	"context"
	"sync"
	"time"
)

// Intent is a strategy's desire to place an order. The loop produces these;
// the order worker consumes them.
type Intent struct {
	MarketTicker string
	Strategy     string
	Action       string // "buy" or "sell"
	PriceCents   int
	ConvProbBps  int    // model probability in basis points
	Reason       string // human-readable trigger description
}

// Handler processes events for a single match. Implemented by the strategy
// set. OnEvent is called from the loop goroutine only — no concurrent access.
type Handler interface {
	OnEvent(ev Event) []Intent
}

// Loop is the single-threaded event loop for one match. Events are enqueued
// from multiple sources (WS, scraper, timer); the loop goroutine dequeues
// and dispatches to the handler. Intents flow to the sink.
//
// Enqueue blocks when the queue is full — never drops. A blocked-nanos
// counter tracks backpressure.
type Loop struct {
	queue       chan Event
	handler     Handler
	sink        func([]Intent)
	blockedNs   int64
}

// NewLoop creates a loop with a buffered queue of capacity 4096.
func NewLoop(h Handler, sink func([]Intent)) *Loop {
	return &Loop{
		queue:   make(chan Event, 4096),
		handler: h,
		sink:    sink,
	}
}

// Enqueue pushes an event onto the queue. Blocks when full — never drops.
// Increments blockedNs by the time spent waiting.
func (l *Loop) Enqueue(ev Event) {
	for {
		select {
		case l.queue <- ev:
			return
		default:
			start := time.Now()
			l.queue <- ev
			atomicAddBlockedNs(l, time.Since(start).Nanoseconds())
			return
		}
	}
}

// Run drains the queue and dispatches to the handler until ctx is cancelled.
// Single goroutine — all handler invocations happen here.
func (l *Loop) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-l.queue:
			intents := l.handler.OnEvent(ev)
			if len(intents) > 0 {
				l.sink(intents)
			}
		}
	}
}

// BlockedNs returns the total nanoseconds spent blocked on a full queue.
func (l *Loop) BlockedNs() int64 {
	return atomicLoadBlockedNs(l)
}

// atomic helpers for blockedNs — avoids pulling sync/atomic into the API.
func atomicAddBlockedNs(l *Loop, d int64) {
	blockedNsMu.Lock()
	l.blockedNs += d
	blockedNsMu.Unlock()
}

func atomicLoadBlockedNs(l *Loop) int64 {
	blockedNsMu.Lock()
	v := l.blockedNs
	blockedNsMu.Unlock()
	return v
}

var blockedNsMu sync.Mutex
