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
	CooldownSecs int     // per-market cooldown window
	MaxPerSec    int     // global rate limit (orders/sec, 0 = unlimited)
	BudgetTotal  float64 // starting budget in dollars (0 = no budget tracking)
	BudgetFloor  float64 // stop ordering when remaining drops below this
}

// QuotaGuard wraps two emitters: a paper trail emitter (always receives all
// orders) and an inner emitter (receives only quota-approved orders).
// Implements OrderEmitter.
//
// When Enabled is false, all orders pass to paper only — inner is expected
// to be NoopEmitter. This preserves current paper-trading behavior.
//
// When Enabled is true, applies four layers of throttling before forwarding
// to inner:
//  1. Per-market cooldown — first order per market within window passes,
//     rest dropped. Prevents N strategies from firing N orders on same market.
//  2. Budget floor — tracks cumulative spend locally. If remaining budget
//     would drop below floor, order is dropped. No REST balance query needed.
//  3. Global rate limit — token bucket caps orders/sec across all markets.
//     Non-blocking: drops if no token available (never blocks WS goroutine).
type QuotaGuard struct {
	paper OrderEmitter
	inner OrderEmitter
	cfg   QuotaConfig
	log   *slog.Logger

	mu              sync.Mutex
	lastOrder       map[string]time.Time // per-market-per-strategy
	lastOrderMarket map[string]time.Time // per-market

	tokens chan struct{}
	stop   chan struct{}
	closed sync.Once

	spent atomic.Int64 // cumulative spend in cents (for budget tracking)
}

// NewQuotaGuard creates a quota-throttling emitter wrapper.
// paper always receives every order. inner receives only approved orders
// when Enabled is true.
func NewQuotaGuard(paper, inner OrderEmitter, cfg QuotaConfig, log *slog.Logger) *QuotaGuard {
	q := &QuotaGuard{
		paper:           paper,
		inner:           inner,
		cfg:             cfg,
		log:             log,
		lastOrder:       make(map[string]time.Time),
		lastOrderMarket: make(map[string]time.Time),
		stop:            make(chan struct{}),
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

	return q
}

func (q *QuotaGuard) EmitOrder(o store.Order) bool {
	// always paper trail — complete signal log regardless of quota
	q.paper.EmitOrder(o)

	if !q.cfg.Enabled {
		return true
	}

	// 1a. per-market cooldown
	q.mu.Lock()
	if last, ok := q.lastOrderMarket[o.MarketTicker]; ok {
		cooldown := time.Duration(q.cfg.CooldownSecs) * time.Second
		if time.Since(last) < cooldown {
			q.mu.Unlock()
			q.log.Debug("quota: dropped, market in cooldown",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"age_ms", time.Since(last).Milliseconds())
			return false
		}
	}

	// 1b. per-market-per-strategy cooldown
	cooldownKey := o.MarketTicker + "|" + o.Strategy
	if last, ok := q.lastOrder[cooldownKey]; ok {
		cooldown := time.Duration(q.cfg.CooldownSecs) * time.Second
		if time.Since(last) < cooldown {
			q.mu.Unlock()
			q.log.Debug("quota: dropped, strategy in cooldown",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"age_ms", time.Since(last).Milliseconds())
			return false
		}
	}
	q.lastOrder[cooldownKey] = time.Now()
	q.lastOrderMarket[o.MarketTicker] = time.Now()
	q.mu.Unlock()

	// 2. budget floor — track spend locally, scale-to-fit if over budget
	if q.cfg.BudgetTotal > 0 {
		orderCents := int64(o.SuggestedSize * 100)
		if orderCents <= 0 {
			return false
		}
		newSpent := q.spent.Add(orderCents)
		remainingCents := int64(q.cfg.BudgetTotal*100) - newSpent
		floorCents := int64(q.cfg.BudgetFloor * 100)
		if remainingCents < floorCents {
			q.spent.Add(-orderCents) // rollback full amount
			availCents := int64(q.cfg.BudgetTotal*100) - q.spent.Load() - floorCents
			if availCents <= 0 {
				q.log.Warn("quota: budget exhausted, dropped",
					"market", o.MarketTicker, "strategy", o.Strategy,
					"remaining", float64(q.spent.Load())/100,
					"floor", q.cfg.BudgetFloor)
				return false
			}
			scaledSize := float64(availCents) / 100
			o.SuggestedSize = scaledSize
			q.spent.Add(int64(scaledSize * 100))
			q.log.Warn("quota: scaled order to fit budget",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"orig_size", float64(orderCents)/100,
				"scaled_size", scaledSize,
				"remaining", float64(int64(q.cfg.BudgetTotal*100)-q.spent.Load())/100)
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

	remainingBudget := -1.0
	if q.cfg.BudgetTotal > 0 {
		remainingBudget = q.cfg.BudgetTotal - float64(q.spent.Load())/100
	}

	q.log.Info("quota: order approved",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"edge_cents", o.EdgeCents, "price", o.MarketPrice,
		"remaining_budget", remainingBudget)

	return q.inner.EmitOrder(o)
}

// RemainingBudget returns remaining budget in dollars (-1 = no budget tracking).
func (q *QuotaGuard) RemainingBudget() float64 {
	if q.cfg.BudgetTotal <= 0 {
		return -1
	}
	return q.cfg.BudgetTotal - float64(q.spent.Load())/100
}

// Close stops the rate limiter goroutine. Safe to call once.
func (q *QuotaGuard) Close() {
	q.closed.Do(func() { close(q.stop) })
}

// SetInner replaces the inner emitter. Used to wire realGuard after construction.
func (q *QuotaGuard) SetInner(inner OrderEmitter) {
	q.mu.Lock()
	q.inner = inner
	q.mu.Unlock()
}
