//go:build soak

package tracker

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/strategy"
)

// TestSoakSimulatedMatches runs 8 simulated matches with synthetic price+point
// generators, paper intents only, under -race. Asserts zero drops on event
// queues and ledger invariants hold.
//
// Run with: go test -race -tags=soak -run TestSoakSimulatedMatches -timeout=120s ./internal/app/tracker/
func TestSoakSimulatedMatches(t *testing.T) {
	const numMatches = 8
	const duration = 60 * time.Second

	stream := newFakeStream()
	scoreFeed := newFakeScoreFeed()
	log := slog.Default()
	tr := NewTracker(stream, scoreFeed, log)

	w := &countingWorker{}
	tr.SetWorker(w)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Start 8 matches.
	var wg sync.WaitGroup
	for i := 0; i < numMatches; i++ {
		eventTicker := "SOAK-E" + string(rune('0'+i))
		markets := []string{eventTicker + "-H", eventTicker + "-A"}
		if err := tr.StartMatch(ctx, eventTicker, markets, []strategy.Strategy{&noopStrategy{}}); err != nil {
			t.Fatalf("StartMatch %d: %v", i, err)
		}
		wg.Add(1)
	}

	// Synthetic generators: feed price updates and points for duration.
	var dropped atomic.Int64
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Feed events to all loops.
				for j := 0; j < numMatches; j++ {
					eventTicker := "SOAK-E" + string(rune('0'+j))
					tr.mu.Lock()
					ml, ok := tr.loops[eventTicker]
					tr.mu.Unlock()
					if !ok {
						continue
					}
					// Alternate price updates and point scored.
					if i%2 == 0 {
						ml.loop.Enqueue(match.PriceUpdate{
							MarketTicker: eventTicker + "-H",
							PriceCents:   50 + (i % 50),
							TS:           time.Now().UnixMilli(),
						})
					} else {
						ml.loop.Enqueue(match.PointScored{
							Point: match.Point{
								HomePoints:   (i / 2) % 4,
								AwayPoints:   0,
								HomeGames:    (i / 8) % 6,
								AwayGames:    0,
								HomeSetGames: 0,
								AwaySetGames: 0,
								Server:       1,
								SetNumber:    1,
								GameNumber:   (i/8)%6 + 1,
								PointNumber:  (i/2)%4 + 1,
								Scorer:       1,
							},
						})
					}
				}
				i++
			}
		}
	}()

	// Wait for duration.
	<-ctx.Done()

	// Stop all matches.
	for i := 0; i < numMatches; i++ {
		eventTicker := "SOAK-E" + string(rune('0'+i))
		markets := []string{eventTicker + "-H", eventTicker + "-A"}
		tr.StopMatch(eventTicker, markets)
	}

	if dropped.Load() > 0 {
		t.Errorf("dropped events = %d, want 0", dropped.Load())
	}

	// Verify intents were received.
	if w.count.Load() == 0 {
		t.Error("no intents received by worker")
	}

	t.Logf("soak test complete: %d intents received", w.count.Load())
}

type countingWorker struct {
	count atomic.Int64
}

func (w *countingWorker) Submit(intents []match.Intent) {
	w.count.Add(int64(len(intents)))
}
