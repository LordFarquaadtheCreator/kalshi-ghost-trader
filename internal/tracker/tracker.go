package tracker

import (
	"context"
	"log/slog"
	"sync"

	wsclient "github.com/farquaad/kalshi-ghost-trader/internal/ws"
)

// Tracker manages active market subscriptions. Each tracked market has
// a WS subscription; stopping removes it. No per-match goroutine —
// ticks are stored directly by the WS manager's tick writer.
type Tracker struct {
	ws   *wsclient.Manager
	log  *slog.Logger
	mu   sync.Mutex
	subs map[string]struct{}
}

// New creates a Tracker.
func New(ws *wsclient.Manager, log *slog.Logger) *Tracker {
	return &Tracker{
		ws:   ws,
		log:  log,
		subs: make(map[string]struct{}),
	}
}

// StartMatch begins tracking a market. Idempotent — already-tracked returns nil.
func (t *Tracker) StartMatch(ctx context.Context, market string) error {
	t.mu.Lock()
	if _, ok := t.subs[market]; ok {
		t.mu.Unlock()
		return nil
	}
	t.subs[market] = struct{}{}
	t.mu.Unlock()

	if err := t.ws.Subscribe(ctx, market); err != nil {
		t.mu.Lock()
		delete(t.subs, market)
		t.mu.Unlock()
		return err
	}
	t.log.Info("started match", "market", market)
	return nil
}

// StopMatch stops tracking a market.
func (t *Tracker) StopMatch(market string) {
	t.mu.Lock()
	if _, ok := t.subs[market]; !ok {
		t.mu.Unlock()
		return
	}
	delete(t.subs, market)
	t.mu.Unlock()

	t.ws.Unsubscribe(context.Background(), market)
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
