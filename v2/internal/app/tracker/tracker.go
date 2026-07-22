// Package tracker manages the per-match lifecycle: discovery, loop creation,
// stream subscription, and teardown on settlement.
package tracker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/strategy"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

// Tracker manages active match loops. One loop per active match.
type Tracker struct {
	mu       sync.Mutex
	loops    map[string]*matchLoop // event_ticker → loop
	stream   ports.MarketStream
	scoreFeed ports.ScoreFeed
	worker   orderWorker
	log      *slog.Logger
}

// orderWorker is a minimal interface for the order worker.
type orderWorker interface {
	Submit(intents []match.Intent)
}

// matchLoop holds a loop and its cancel function.
type matchLoop struct {
	loop   *match.Loop
	cancel context.CancelFunc
}

// NewTracker creates a tracker.
func NewTracker(stream ports.MarketStream, scoreFeed ports.ScoreFeed, log *slog.Logger) *Tracker {
	return &Tracker{
		loops:     make(map[string]*matchLoop),
		stream:    stream,
		scoreFeed: scoreFeed,
		log:       log,
	}
}

// StartMatch creates a new event loop for a match, subscribes to its markets,
// and begins processing events.
func (t *Tracker) StartMatch(ctx context.Context, eventTicker string, marketTickers []string, strategies []strategy.Strategy) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.loops[eventTicker]; exists {
		return nil // already tracking
	}

	// Create a multi-strategy handler.
	handler := &multiHandler{strategies: strategies, state: &strategy.State{
		MatchView: strategy.MatchView{
			EventTicker:   eventTicker,
			MarketTickers: marketTickers,
			Prices:        make(map[string]int),
			PriceTS:       make(map[string]int64),
		},
		StrategyState: make(map[string]any),
	}}

	// Create the loop with the worker as sink.
	matchCtx, cancel := context.WithCancel(ctx)
	loop := match.NewLoop(handler, func(intents []match.Intent) {
		if t.worker != nil {
			t.worker.Submit(intents)
		}
	})

	t.loops[eventTicker] = &matchLoop{loop: loop, cancel: cancel}

	// Subscribe to market data.
	if t.stream != nil {
		if err := t.stream.Subscribe(marketTickers); err != nil {
			t.log.Error("tracker: subscribe failed", "event", eventTicker, "err", err)
		}
	}

	// Start score polling.
	if t.scoreFeed != nil {
		if err := t.scoreFeed.StartPolling(eventTicker); err != nil {
			t.log.Error("tracker: start polling failed", "event", eventTicker, "err", err)
		}
	}

	// Run the loop in a goroutine.
	go func() {
		if err := loop.Run(matchCtx); err != nil {
			t.log.Debug("tracker: loop exited", "event", eventTicker, "err", err)
		}
	}()

	t.log.Info("tracker: started match", "event", eventTicker, "markets", len(marketTickers))
	return nil
}

// StopMatch tears down a match loop and unsubscribes from markets.
func (t *Tracker) StopMatch(eventTicker string, marketTickers []string) {
	t.mu.Lock()
	ml, ok := t.loops[eventTicker]
	if ok {
		delete(t.loops, eventTicker)
	}
	t.mu.Unlock()

	if !ok {
		return
	}

	ml.cancel()

	if t.stream != nil {
		_ = t.stream.Unsubscribe(marketTickers)
	}
	if t.scoreFeed != nil {
		_ = t.scoreFeed.StopPolling(eventTicker)
	}

	t.log.Info("tracker: stopped match", "event", eventTicker)
}

// SetWorker sets the order worker that receives intents from loops.
func (t *Tracker) SetWorker(w orderWorker) {
	t.worker = w
}

// ActiveMatches returns the count of active match loops.
func (t *Tracker) ActiveMatches() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.loops)
}

// multiHandler dispatches events to all strategies and collects intents.
type multiHandler struct {
	strategies []strategy.Strategy
	state      *strategy.State
}

func (h *multiHandler) OnEvent(ev match.Event) []match.Intent {
	// Update the match view based on event type.
	h.updateView(ev)

	var allIntents []match.Intent
	for _, s := range h.strategies {
		intents := s.OnEvent(ev, h.state)
		allIntents = append(allIntents, intents...)
	}
	return allIntents
}

func (h *multiHandler) updateView(ev match.Event) {
	switch e := ev.(type) {
	case match.PriceUpdate:
		h.state.MatchView.Prices[e.MarketTicker] = e.PriceCents
		h.state.MatchView.PriceTS[e.MarketTicker] = e.TS
	case match.PointScored:
		p := e.Point
		h.state.MatchView.SetsHome = p.HomeSetGames
		h.state.MatchView.SetsAway = p.AwaySetGames
		h.state.MatchView.GamesHome = p.HomeGames
		h.state.MatchView.GamesAway = p.AwayGames
		h.state.MatchView.HomePoints = p.HomePoints
		h.state.MatchView.AwayPoints = p.AwayPoints
		h.state.MatchView.Server = p.Server
		h.state.MatchView.IsTiebreak = p.IsTiebreak
		h.state.MatchView.SetNumber = p.SetNumber
		h.state.MatchView.GameNumber = p.GameNumber
		h.state.MatchView.PointNumber = p.PointNumber
		h.state.MatchView.Scorer = p.Scorer
		h.state.MatchView.IsBreakPoint = p.IsBreakPoint
		h.state.MatchView.IsSetPoint = p.IsSetPoint
		h.state.MatchView.IsMatchPoint = p.IsMatchPoint
	case match.ClockTick:
		// No view update needed — strategies use ClockTick for timing.
	}
}

// Ensure time is imported (used in future scheduler integration).
var _ = time.Now
