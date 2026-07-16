// Package tracker manages the lifecycle of per-market WebSocket subscriptions.
//
// The Tracker maintains a set of actively tracked market tickers and delegates
// to the ws.Manager for subscribe/unsubscribe operations. No per-match goroutines
// are created — incoming ticks are stored directly by the WS manager's TickWriter.
// The Tracker only tracks which markets are subscribed.
//
// StartMatch is idempotent (already-tracked markets return nil) and rolls back
// the subscription on error. StopMatch sends a real Kalshi unsubscribe command
// with stored sids.
//
// When a ScorePoller is provided, StartMatch/StopMatch also drive
// score polling — polling starts when the first market for an event is
// tracked and stops when the last market for that event is untracked.
package tracker

import (
	"context"
	"log/slog"
	"sync"

	wsclient "github.com/farquaad/kalshi-ghost-trader/internal/ws"
)

// ScorePoller is implemented by score data sources (FlashScore, API-Tennis)
// to receive lifecycle commands driven by market subscriptions.
type ScorePoller interface {
	StartPolling(eventTicker string)
	StopPolling(eventTicker string)
}

// PriceCleaner removes price tracking state for a market. Implemented by
// signal.Generator. Called on unsubscribe to prevent unbounded price map
// growth when FlashScore is disabled (UnregisterMarkets only runs via FS).
type PriceCleaner interface {
	DeletePrice(marketTicker string)
}

// Tracker manages active market subscriptions. Each tracked market has
// a WS subscription; stopping removes it. No per-match goroutine —
// ticks are stored directly by the WS manager's tick writer.
type Tracker struct {
	ws  *wsclient.Manager
	sp  ScorePoller  // nil if no score source enabled
	pc  PriceCleaner // nil if no signal generator
	log *slog.Logger
	mu  sync.Mutex
	// subs maps market_ticker → event_ticker for all tracked markets.
	// event_ticker is used to drive score polling.
	subs map[string]string
}

// New creates a Tracker. sp may be nil to disable score polling coupling.
func New(ws *wsclient.Manager, sp ScorePoller, log *slog.Logger) *Tracker {
	return &Tracker{
		ws:   ws,
		sp:   sp,
		log:  log,
		subs: make(map[string]string),
	}
}

// SetPriceCleaner wires a price cleaner (signal.Generator) to remove
// price tracking state when markets are unsubscribed.
func (t *Tracker) SetPriceCleaner(pc PriceCleaner) {
	t.pc = pc
}

// StartMatch begins tracking a market. Idempotent — already-tracked returns nil.
// eventTicker associates the market with its parent event for FlashScore polling.
func (t *Tracker) StartMatch(ctx context.Context, market, eventTicker string) error {
	t.mu.Lock()
	if _, ok := t.subs[market]; ok {
		t.mu.Unlock()
		return nil
	}
	t.subs[market] = eventTicker
	t.mu.Unlock()

	if err := t.ws.Subscribe(ctx, market); err != nil {
		t.mu.Lock()
		delete(t.subs, market)
		t.mu.Unlock()
		return err
	}

	// Start score polling for this event (if not already active)
	if t.sp != nil {
		t.sp.StartPolling(eventTicker)
	}

	t.log.Info("started match", "market", market, "event", eventTicker)
	return nil
}

// StopMatch stops tracking a market.
func (t *Tracker) StopMatch(market string) {
	t.mu.Lock()
	eventTicker, ok := t.subs[market]
	if !ok {
		t.mu.Unlock()
		return
	}
	delete(t.subs, market)

	// Check if any other tracked market shares this event
	eventStillTracked := false
	for _, ev := range t.subs {
		if ev == eventTicker {
			eventStillTracked = true
			break
		}
	}
	t.mu.Unlock()

	t.ws.Unsubscribe(context.Background(), market)

	// Clean up price tracking state for this market
	if t.pc != nil {
		t.pc.DeletePrice(market)
	}

	// Stop score polling only if no other market for this event is tracked
	if t.sp != nil && !eventStillTracked {
		t.sp.StopPolling(eventTicker)
	}

	t.log.Info("stopped match", "market", market)
}

// ActiveMarkets returns currently tracked market tickers.
func (t *Tracker) ActiveMarkets() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.subs))
	for k := range t.subs {
		out = append(out, k)
	}
	return out
}

// ActiveEvents returns event tickers for all tracked markets (deduplicated).
func (t *Tracker) ActiveEvents() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := make(map[string]struct{}, len(t.subs))
	for _, ev := range t.subs {
		seen[ev] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for ev := range seen {
		out = append(out, ev)
	}
	return out
}

// StopAll stops all tracked markets.
func (t *Tracker) StopAll() {
	t.mu.Lock()
	markets := make([]string, 0, len(t.subs))
	for k := range t.subs {
		markets = append(markets, k)
	}
	t.mu.Unlock()

	for _, m := range markets {
		t.StopMatch(m)
	}
}
