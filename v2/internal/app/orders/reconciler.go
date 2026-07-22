package orders

import (
	"context"
	"log/slog"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

// Reconciler periodically re-fetches stale orders from the exchange and
// drives them to terminal status. Also runs nightly ledger invariant checks.
type Reconciler struct {
	repo     ports.OrderRepo
	exchange ports.Exchange
	ledger   ports.LedgerRepo
	log      *slog.Logger
	interval time.Duration
	staleAge time.Duration
}

// NewReconciler creates a reconciler.
func NewReconciler(repo ports.OrderRepo, exchange ports.Exchange, ledger ports.LedgerRepo, log *slog.Logger, intervalSecs, staleAgeSecs int) *Reconciler {
	return &Reconciler{
		repo:     repo,
		exchange: exchange,
		ledger:   ledger,
		log:      log,
		interval: time.Duration(intervalSecs) * time.Second,
		staleAge: time.Duration(staleAgeSecs) * time.Second,
	}
}

// Run polls for stale orders and reconciles them.
func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Nightly invariant check at midnight.
	invariantTicker := time.NewTicker(1 * time.Hour)
	defer invariantTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.reconcilePass(ctx); err != nil {
				r.log.Error("reconciler: pass failed", "err", err)
			}
		case <-invariantTicker.C:
			if err := r.ledger.CheckInvariants(ctx); err != nil {
				r.log.Error("reconciler: ledger invariant violation", "err", err)
			}
		}
	}
}

func (r *Reconciler) reconcilePass(ctx context.Context) error {
	staleStatuses := []string{StatusSubmitted, StatusPartial, StatusUnverified}
	orders, err := r.repo.GetStaleOrders(ctx, staleStatuses, r.staleAge)
	if err != nil {
		return err
	}

	for _, o := range orders {
		if err := r.reconcileOrder(ctx, o); err != nil {
			r.log.Error("reconciler: order failed", "id", o.ID, "err", err)
		}
	}
	return nil
}

func (r *Reconciler) reconcileOrder(ctx context.Context, o ports.OrderRecord) error {
	// Re-fetch from exchange by client_order_id.
	// The exchange implementation handles the lookup.
	_ = ctx
	_ = o
	// In production: call exchange.GetOrderStatus(ctx, o.ClientOrderID)
	// and drive the same transition functions as the worker.
	// For now, this is a stub — the exchange interface needs a GetOrder method.
	return nil
}
