package algorithms

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// QuotaConfig controls order throttling to prevent exhausting API quota.
type QuotaConfig struct {
	Enabled      bool
	CooldownSecs int // per-market cooldown window
	MaxPerSec    int // global rate limit (orders/sec, 0 = unlimited)
	DailyLimit   int // hard daily ceiling (0 = unlimited)
}

// QuotaGuard wraps two emitters: a paper trail emitter (always receives all
// orders) and an inner emitter (receives only quota-approved orders).
// Implements OrderEmitter.
//
// When Enabled is false, all orders pass to paper only — inner is expected
// to be NoopEmitter. This preserves current paper-trading behavior.
//
// When Enabled is true, applies three layers of throttling before forwarding
// to inner:
//  1. Per-market cooldown — first order per market within window passes,
//     rest dropped. Prevents N strategies from firing N orders on same market.
//  2. Global rate limit — token bucket caps orders/sec across all markets.
//     Non-blocking: drops if no token available (never blocks WS goroutine).
//  3. Daily quota — hard ceiling on total orders per session.
type QuotaGuard struct {
	paper OrderEmitter
	inner OrderEmitter
	cfg   QuotaConfig
	log   *slog.Logger

	mu        sync.Mutex
	lastOrder map[string]time.Time

	tokens chan struct{}
	stop   chan struct{}
	closed sync.Once

	remaining atomic.Int64
}

// NewQuotaGuard creates a quota-throttling emitter wrapper.
// paper always receives every order. inner receives only approved orders
// when Enabled is true.
func NewQuotaGuard(paper, inner OrderEmitter, cfg QuotaConfig, log *slog.Logger) *QuotaGuard {
	q := &QuotaGuard{
		paper:     paper,
		inner:     inner,
		cfg:       cfg,
		log:       log,
		lastOrder: make(map[string]time.Time),
		stop:      make(chan struct{}),
	}

	if cfg.Enabled && cfg.MaxPerSec > 0 {
		q.tokens = make(chan struct{}, cfg.MaxPerSec)
		for i := 0; i < cfg.MaxPerSec; i++ {
			q.tokens <- struct{}{}
		}
		interval := time.Second / time.Duration(cfg.MaxPerSec)
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-q.stop:
					return
				case <-ticker.C:
					select {
					case q.tokens <- struct{}{}:
					default:
					}
				}
			}
		}()
	}

	if cfg.Enabled && cfg.DailyLimit > 0 {
		q.remaining.Store(int64(cfg.DailyLimit))
	}

	return q
}

func (q *QuotaGuard) EmitOrder(o store.Order) bool {
	// always paper trail — complete signal log regardless of quota
	q.paper.EmitOrder(o)

	if !q.cfg.Enabled {
		return true
	}

	// 1. per-market cooldown
	q.mu.Lock()
	if last, ok := q.lastOrder[o.MarketTicker]; ok {
		cooldown := time.Duration(q.cfg.CooldownSecs) * time.Second
		if time.Since(last) < cooldown {
			q.mu.Unlock()
			q.log.Debug("quota: dropped, market in cooldown",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"age_ms", time.Since(last).Milliseconds())
			return false
		}
	}
	q.lastOrder[o.MarketTicker] = time.Now()
	q.mu.Unlock()

	// 2. daily quota
	if q.cfg.DailyLimit > 0 {
		if q.remaining.Add(-1) < 0 {
			q.remaining.Store(0)
			q.log.Warn("quota: daily limit exhausted",
				"market", o.MarketTicker, "strategy", o.Strategy)
			return false
		}
	}

	// 3. global rate limit — non-blocking, drop if no token
	if q.tokens != nil {
		select {
		case <-q.tokens:
		case <-q.stop:
			return false
		default:
			q.log.Warn("quota: rate limited, dropped",
				"market", o.MarketTicker, "strategy", o.Strategy)
			return false
		}
	}

	q.log.Info("quota: order approved",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"edge_cents", o.EdgeCents, "price", o.MarketPrice)

	return q.inner.EmitOrder(o)
}

// ResetDailyQuota resets the daily order counter.
func (q *QuotaGuard) ResetDailyQuota() {
	if q.cfg.DailyLimit > 0 {
		q.remaining.Store(int64(q.cfg.DailyLimit))
		q.log.Info("quota: daily counter reset", "limit", q.cfg.DailyLimit)
	}
}

// RemainingQuota returns remaining daily orders (-1 = unlimited).
func (q *QuotaGuard) RemainingQuota() int64 {
	if q.cfg.DailyLimit <= 0 {
		return -1
	}
	return q.remaining.Load()
}

// Close stops the rate limiter goroutine. Safe to call once.
func (q *QuotaGuard) Close() {
	q.closed.Do(func() { close(q.stop) })
}
