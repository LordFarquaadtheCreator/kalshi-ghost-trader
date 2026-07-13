package kalshiclient

import (
	"context"
	"time"
)

// rateLimiter is a simple token-bucket throttle. Allows up to rps requests
// per second with bursting. Blocks when bucket empty.
type rateLimiter struct {
	tokens chan struct{}
	stop   chan struct{}
}

func newRateLimiter(rps int) *rateLimiter {
	rl := &rateLimiter{
		tokens: make(chan struct{}, rps),
		stop:   make(chan struct{}),
	}
	// Pre-fill bucket for initial burst
	for i := 0; i < rps; i++ {
		rl.tokens <- struct{}{}
	}
	// Refill at rps tokens/sec
	interval := time.Second / time.Duration(rps)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-rl.stop:
				return
			case <-ticker.C:
				select {
				case rl.tokens <- struct{}{}:
				default: // bucket full, drop token
				}
			}
		}
	}()
	return rl
}

func (rl *rateLimiter) wait(ctx context.Context) error {
	select {
	case <-rl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rl *rateLimiter) close() {
	close(rl.stop)
}
