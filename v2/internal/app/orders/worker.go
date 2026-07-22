package orders

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/sizing"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
	"github.com/google/uuid"
)

// Worker receives intents from the match loop and processes them through
// the order pipeline: persist → gate → size → hold → submit → fill.
// Single goroutine — no concurrent access to the exchange or ledger.
type Worker struct {
	in        chan match.Intent
	gates     *GateCache
	ledger    ports.LedgerRepo
	exchange  ports.Exchange
	repo      ports.OrderRepo
	featureRepo ports.FeatureRepo
	log       *slog.Logger
	bankroll  int64 // current bankroll in cents (for sizing)
	kellyFrac float64
	legacySizing bool
	dropped   atomic.Int64

	// per-strategy config for sizing
	perStrategyFraction map[string]float64
}

// NewWorker creates an order worker.
func NewWorker(
	gates *GateCache,
	ledger ports.LedgerRepo,
	exchange ports.Exchange,
	repo ports.OrderRepo,
	featureRepo ports.FeatureRepo,
	log *slog.Logger,
	bankrollCents int64,
	kellyFraction float64,
	legacySizing bool,
) *Worker {
	return &Worker{
		in:                 make(chan match.Intent, 1024),
		gates:              gates,
		ledger:             ledger,
		exchange:           exchange,
		repo:               repo,
		featureRepo:        featureRepo,
		log:                log,
		bankroll:           bankrollCents,
		kellyFrac:          kellyFraction,
		legacySizing:       legacySizing,
		perStrategyFraction: make(map[string]float64),
	}
}

// Submit is the loop's sink. Non-blocking enqueue; drops+counts if full.
func (w *Worker) Submit(intents []match.Intent) {
	for _, i := range intents {
		select {
		case w.in <- i:
		default:
			w.dropped.Add(1)
			w.log.Warn("orders: intent queue full, dropping", "strategy", i.Strategy, "market", i.MarketTicker)
		}
	}
}

// Dropped returns the count of dropped intents due to full queue.
func (w *Worker) Dropped() int64 { return w.dropped.Load() }

// SetBankroll updates the bankroll used for sizing.
func (w *Worker) SetBankroll(cents int64) {
	w.bankroll = cents
}

// Run drains the intent queue and processes each intent through the pipeline.
// Single goroutine.
func (w *Worker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case i := <-w.in:
			if err := w.processIntent(ctx, i); err != nil {
				w.log.Error("orders: process intent failed", "strategy", i.Strategy, "market", i.MarketTicker, "err", err)
			}
		}
	}
}

// processIntent drives one intent through the full pipeline.
func (w *Worker) processIntent(ctx context.Context, i match.Intent) error {
	now := time.Now()
	isPaper := !w.realTradingEnabled()

	// Persist as intent.
	rec := ports.OrderRecord{
		EventTicker:  "", // filled by caller context
		MarketTicker: i.MarketTicker,
		Strategy:     i.Strategy,
		Action:       i.Action,
		PriceCents:   i.PriceCents,
		ConvProbBps:  i.ConvProbBps,
		Reason:       i.Reason,
		Status:       StatusIntent,
		IsPaper:      isPaper,
		TSIntent:     now.UnixMilli(),
	}
	rec.ClientOrderID = uuid.NewString()

	id, err := w.repo.Insert(ctx, rec)
	if err != nil {
		return err
	}

	// Log features for every intent — including gated ones (A.2.1).
	if w.featureRepo != nil && i.FeatureHash != "" {
		if err := w.featureRepo.LogFeatures(ctx, id, ports.FeatureLog{
			FeatureHash: i.FeatureHash,
			Features:    i.Features,
			ModelID:     i.ModelID,
			Propensity:  i.Propensity,
		}); err != nil {
			w.log.Error("orders: log features failed", "order_id", id, "err", err)
		}
	}

	// Evaluate gates.
	gateReason := w.gates.Evaluate(i.Strategy, i.PriceCents, i.MarketTicker, isPaper, now)
	if gateReason != "" {
		return w.repo.UpdateStatus(ctx, id, StatusGated, ports.UpdateOpts{GateReason: gateReason})
	}

	// Size via Kelly.
	frac := w.kellyFrac
	if f, ok := w.perStrategyFraction[i.Strategy]; ok {
		frac = f
	}
	contracts := sizing.KellyContracts(i.ConvProbBps, i.PriceCents, w.bankroll, frac, w.legacySizing)
	if contracts <= 0 {
		return w.repo.UpdateStatus(ctx, id, StatusGated, ports.UpdateOpts{GateReason: GateInsufficientBalance})
	}

	// Accepted.
	if err := w.repo.UpdateStatus(ctx, id, StatusAccepted, ports.UpdateOpts{}); err != nil {
		return err
	}
	w.gates.OnOrderAccepted(i.MarketTicker)

	if isPaper {
		// Paper path: accepted → filled at intent price.
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, true, now)
		return w.repo.UpdateStatus(ctx, id, StatusFilled, ports.UpdateOpts{
			FillCount:      contracts,
			FillPriceCents: i.PriceCents,
		})
	}

	// Real path: hold → submit → fill.
	spendCents := int64(contracts) * int64(i.PriceCents)
	if err := w.ledger.HoldForOrder(ctx, id, spendCents); err != nil {
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, false, now)
		return w.repo.UpdateStatus(ctx, id, StatusGated, ports.UpdateOpts{GateReason: GateInsufficientBalance})
	}

	if err := w.repo.UpdateStatus(ctx, id, StatusHeld, ports.UpdateOpts{}); err != nil {
		return err
	}

	// Submit to exchange.
	tsSub := now.UnixMilli()
	resp, err := w.exchange.CreateOrder(ctx, ports.CreateOrderRequest{
		ClientOrderID: rec.ClientOrderID,
		MarketTicker:  i.MarketTicker,
		Action:        i.Action,
		Contracts:     contracts,
		PriceCents:    i.PriceCents,
	})
	if err != nil {
		// Exchange error → release hold, mark failed.
		_ = w.ledger.ReleaseHold(ctx, id, spendCents)
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, false, now)
		_ = w.repo.UpdateStatus(ctx, id, StatusSubmitted, ports.UpdateOpts{TSSubmitted: &tsSub})
		return w.repo.UpdateStatus(ctx, id, StatusFailed, ports.UpdateOpts{})
	}

	// Mark submitted.
	if err := w.repo.UpdateStatus(ctx, id, StatusSubmitted, ports.UpdateOpts{TSSubmitted: &tsSub}); err != nil {
		return err
	}

	tsAck := time.Now().UnixMilli()

	switch resp.Status {
	case ports.OrderStatusFilled:
		fillCost := int64(resp.FillCount) * int64(resp.FillPriceCents)
		// Release the unfilled remainder.
		remainder := spendCents - fillCost
		if remainder > 0 {
			_ = w.ledger.ReleaseHold(ctx, id, remainder)
		}
		_ = w.ledger.RecordFill(ctx, id, fillCost)
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, true, now)
		return w.repo.UpdateStatus(ctx, id, StatusFilled, ports.UpdateOpts{
			KalshiOrderID:  resp.OrderID,
			FillCount:      resp.FillCount,
			FillPriceCents: resp.FillPriceCents,
			TSAcked:        &tsAck,
		})

	case ports.OrderStatusPartial:
		fillCost := int64(resp.FillCount) * int64(resp.FillPriceCents)
		remainder := spendCents - fillCost
		if remainder > 0 {
			_ = w.ledger.ReleaseHold(ctx, id, remainder)
		}
		_ = w.ledger.RecordFill(ctx, id, fillCost)
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, true, now)
		return w.repo.UpdateStatus(ctx, id, StatusPartial, ports.UpdateOpts{
			KalshiOrderID:  resp.OrderID,
			FillCount:      resp.FillCount,
			FillPriceCents: resp.FillPriceCents,
			TSAcked:        &tsAck,
		})

	case ports.OrderStatusCanceled:
		_ = w.ledger.ReleaseHold(ctx, id, spendCents)
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, false, now)
		return w.repo.UpdateStatus(ctx, id, StatusCanceled, ports.UpdateOpts{
			KalshiOrderID: resp.OrderID,
			TSAcked:       &tsAck,
		})

	default:
		// Unknown status → unverified.
		w.gates.OnOrderTerminal(i.Strategy, i.MarketTicker, false, now)
		return w.repo.UpdateStatus(ctx, id, StatusUnverified, ports.UpdateOpts{
			KalshiOrderID: resp.OrderID,
			TSAcked:       &tsAck,
		})
	}
}

// realTradingEnabled returns true if real trading is on.
func (w *Worker) realTradingEnabled() bool {
	// In production this reads from config. For now, infer from exchange presence.
	return w.exchange != nil
}
