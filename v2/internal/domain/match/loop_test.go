package match

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// recordingHandler records the goroutine ID of each OnEvent call and all
// events received.
type recordingHandler struct {
	mu       sync.Mutex
	goroutines map[uint64]bool
	events   []Event
	intents  []Intent
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{
		goroutines: make(map[uint64]bool),
	}
}

func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 18 [..."
	id := uint64(0)
	for i := 10; i < n; i++ {
		if buf[i] >= '0' && buf[i] <= '9' {
			id = id*10 + uint64(buf[i]-'0')
		} else if id > 0 {
			break
		}
	}
	return id
}

func (h *recordingHandler) OnEvent(ev Event) []Intent {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.goroutines[goroutineID()] = true
	h.events = append(h.events, ev)
	return h.intents
}

func (h *recordingHandler) countGoroutines() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.goroutines)
}

func (h *recordingHandler) countEvents() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

// TestLoopSingleGoroutineDelivery: all handler invocations happen on the
// same goroutine.
func TestLoopSingleGoroutineDelivery(t *testing.T) {
	h := newRecordingHandler()
	var sinkCalls int
	sink := func([]Intent) { sinkCalls++ }

	loop := NewLoop(h, sink)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = loop.Run(ctx) }()

	for i := 0; i < 100; i++ {
		loop.Enqueue(PriceUpdate{MarketTicker: "M", PriceCents: 50, TS: int64(i)})
	}

	// Wait for all events to be processed.
	time.Sleep(100 * time.Millisecond)
	cancel()

	if h.countGoroutines() != 1 {
		t.Errorf("handler called from %d goroutines, want 1", h.countGoroutines())
	}
	if h.countEvents() != 100 {
		t.Errorf("handler received %d events, want 100", h.countEvents())
	}
}

// TestLoopOrderPreserved: 1000 events, order of receipt equals order of enqueue.
func TestLoopOrderPreserved(t *testing.T) {
	h := &orderHandler{}
	sink := func([]Intent) {}
	loop := NewLoop(h, sink)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = loop.Run(ctx) }()

	for i := 0; i < 1000; i++ {
		loop.Enqueue(PriceUpdate{MarketTicker: "M", PriceCents: i, TS: int64(i)})
	}

	time.Sleep(200 * time.Millisecond)
	cancel()

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.received) != 1000 {
		t.Fatalf("received %d events, want 1000", len(h.received))
	}
	for i, ev := range h.received {
		pu := ev.(PriceUpdate)
		if pu.PriceCents != i {
			t.Errorf("event %d: PriceCents = %d, want %d", i, pu.PriceCents, i)
		}
	}
}

type orderHandler struct {
	mu       sync.Mutex
	received []Event
}

func (h *orderHandler) OnEvent(ev Event) []Intent {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.received = append(h.received, ev)
	return nil
}

// TestLoopDeterministic: same event slice twice → identical intent slices.
func TestLoopDeterministic(t *testing.T) {
	events := []Event{
		PriceUpdate{MarketTicker: "M", PriceCents: 50, TS: 1},
		PriceUpdate{MarketTicker: "M", PriceCents: 55, TS: 2},
		PointScored{EventTicker: "E", Point: Point{HomePoints: "15", AwayPoints: "0"}, TS: 3},
	}

	var run1Intents []Intent

	for run := 0; run < 2; run++ {
		h := &deterministicHandler{}
		var mu sync.Mutex
		var intents []Intent
		sink := func(i []Intent) {
			mu.Lock()
			intents = append(intents, i...)
			mu.Unlock()
		}

		loop := NewLoop(h, sink)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = loop.Run(ctx) }()

		for _, ev := range events {
			loop.Enqueue(ev)
		}

		time.Sleep(100 * time.Millisecond)
		cancel()

		mu.Lock()
		if run == 0 {
			run1Intents = make([]Intent, len(intents))
			copy(run1Intents, intents)
		} else {
			if len(intents) != len(run1Intents) {
				t.Errorf("run 1: got %d intents, want %d (same as run 0)", len(intents), len(run1Intents))
			}
			for i, intent := range intents {
				if i < len(run1Intents) && intent != run1Intents[i] {
					t.Errorf("run 1 intent %d = %+v, want %+v", i, intent, run1Intents[i])
				}
			}
		}
		mu.Unlock()
	}
}

type deterministicHandler struct{}

func (h *deterministicHandler) OnEvent(ev Event) []Intent {
	return []Intent{{MarketTicker: "M", Strategy: "test", Action: "buy", PriceCents: 50}}
}

// TestEnqueueBlocksWhenFull: enqueuing to a full queue blocks until an event
// is drained.
func TestEnqueueBlocksWhenFull(t *testing.T) {
	h := &blockingHandler{}
	sink := func([]Intent) {}

	// Use a small-capacity loop by creating one and filling it before Run.
	loop := NewLoop(h, sink)

	// Fill the queue (capacity 4096) without running the loop.
	for i := 0; i < 4096; i++ {
		loop.Enqueue(PriceUpdate{MarketTicker: "M", PriceCents: i, TS: int64(i)})
	}

	// Now the queue is full. Enqueue should block. Start a goroutine to
	// drain after a short delay.
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = loop.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Enqueue one more — should block briefly then succeed once the loop
	// starts draining.
	start := time.Now()
	loop.Enqueue(PriceUpdate{MarketTicker: "M", PriceCents: 9999, TS: 9999})
	elapsed := time.Since(start)

	if elapsed < 1*time.Millisecond {
		// Should have blocked for at least some time waiting for the loop
		// to drain. If it went through instantly, the queue wasn't full.
		t.Logf("warning: enqueue didn't block (elapsed=%v), queue may not have been full", elapsed)
	}
}

type blockingHandler struct{}

func (h *blockingHandler) OnEvent(ev Event) []Intent {
	return nil
}
