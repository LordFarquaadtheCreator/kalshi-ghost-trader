// Package pnltracker spawns per-order goroutines that compute live
// mark-to-market PnL for filled real orders every 30 seconds.
//
// On each poll, the manager goroutine finds filled buy/buy_no real orders
// with open positions and spawns a tracker goroutine per order. Each
// tracker queries the latest WS tick price for the market, computes
// unrealized PnL = (current_price - fill_price) * fill_count * 100, and
// writes it into resolved_pnl_cents. At settlement, ResolveRealOrders
// overwrites with final realized PnL.
//
// Tracker goroutines stop when the position transitions to settled or
// closed, or when the root context is cancelled.
package pnltracker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// updateInterval is how often each per-order goroutine computes PnL.
const updateInterval = 30 * time.Second

// scanInterval is how often the manager polls for newly filled orders.
const scanInterval = 10 * time.Second

// Tracker manages per-order PnL goroutines.
type Tracker struct {
	db  *store.DB
	log *slog.Logger

	mu      sync.Mutex
	active  map[int64]context.CancelFunc // orderID -> cancel
}

// New creates a Tracker.
func New(db *store.DB, log *slog.Logger) *Tracker {
	return &Tracker{
		db:     db,
		log:    log,
		active: make(map[int64]context.CancelFunc),
	}
}

// Run polls for newly filled real orders and spawns tracker goroutines.
// Blocks until ctx cancelled.
func (t *Tracker) Run(ctx context.Context) error {
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()

	t.scan(ctx)

	for {
		select {
		case <-ctx.Done():
			t.cancelAll()
			return ctx.Err()
		case <-ticker.C:
			t.scan(ctx)
		}
	}
}

func (t *Tracker) scan(ctx context.Context) {
	orders, err := t.db.GetTrackableRealOrders(ctx)
	if err != nil {
		t.log.Error("pnltracker: get trackable orders", "err", err)
		return
	}

	for _, o := range orders {
		t.mu.Lock()
		if _, exists := t.active[o.ID]; exists {
			t.mu.Unlock()
			continue
		}
		t.mu.Unlock()

		t.spawn(ctx, o)
	}
}

func (t *Tracker) spawn(ctx context.Context, o store.Order) {
	orderCtx, cancel := context.WithCancel(ctx)

	t.mu.Lock()
	t.active[o.ID] = cancel
	t.mu.Unlock()

	t.log.Info("pnltracker: tracking order",
		"order_id", o.ID,
		"market", o.MarketTicker,
		"strategy", o.Strategy,
		"fill_count", o.FillCount,
		"fill_price", o.FillPrice)

	go t.trackOrder(orderCtx, o)
}

func (t *Tracker) trackOrder(ctx context.Context, o store.Order) {
	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	// immediate first update
	t.updatePnL(ctx, o)

	for {
		select {
		case <-ctx.Done():
			t.remove(o.ID)
			return
		case <-ticker.C:
			done := t.updatePnL(ctx, o)
			if done {
				t.remove(o.ID)
				return
			}
		}
	}
}

// updatePnL computes and writes unrealized PnL for the order.
// Returns true if the position is no longer open (settled/closed) —
// signals the goroutine to exit.
func (t *Tracker) updatePnL(ctx context.Context, o store.Order) bool {
	status, err := t.db.GetPositionStatus(ctx, o.ID)
	if err != nil {
		t.log.Error("pnltracker: get position status",
			"order_id", o.ID, "err", err)
		return false
	}
	if status != store.PositionStatusOpen {
		t.log.Info("pnltracker: position no longer open, stopping",
			"order_id", o.ID, "status", status)
		return true
	}

	lp, err := t.db.GetLatestTickPrice(ctx, o.MarketTicker)
	if err != nil {
		// no ticks yet — skip this cycle
		return false
	}

	currentPrice := lp.YesPrice
	if o.Action == "buy_no" {
		currentPrice = lp.NoPrice
	}

	costPrice := o.FillPrice
	if costPrice == 0 {
		costPrice = o.MarketPrice
	}

	pnlCents := int64((currentPrice - costPrice) * o.FillCount * 100)

	if err := t.db.UpdateOrderUnrealizedPnL(ctx, o.ID, pnlCents, time.Now().UnixMilli()); err != nil {
		t.log.Error("pnltracker: update unrealized pnl",
			"order_id", o.ID, "err", err)
	}

	return false
}

func (t *Tracker) remove(orderID int64) {
	t.mu.Lock()
	delete(t.active, orderID)
	t.mu.Unlock()
}

func (t *Tracker) cancelAll() {
	t.mu.Lock()
	for _, cancel := range t.active {
		cancel()
	}
	t.active = make(map[int64]context.CancelFunc)
	t.mu.Unlock()
}
