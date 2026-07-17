package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// RealOrderConfig controls real order submission to Kalshi.
type RealOrderConfig struct {
	Enabled       bool
	MaxContracts  int    // hard cap on contracts per order
	Environment   string // "demo" or "prod" — logged for safety
	TimeInForce   string // "immediate_or_cancel" or "good_till_canceled"
	OrderTimeoutS int    // per-order HTTP timeout
}

// createOrderV2Request maps to Kalshi's POST /portfolio/events/orders body.
type createOrderV2Request struct {
	Ticker                  string `json:"ticker"`
	Side                    string `json:"side"`
	Count                   string `json:"count"`
	Price                   string `json:"price"`
	TimeInForce             string `json:"time_in_force"`
	SelfTradePreventionType string `json:"self_trade_prevention_type"`
	PostOnly                bool   `json:"post_only"`
	ReduceOnly              bool   `json:"reduce_only"`
}

type createOrderV2Response struct {
	OrderID        string `json:"order_id"`
	FillCount      string `json:"fill_count"`
	RemainingCount string `json:"remaining_count"`
}

// KalshiOrderEmitter submits real orders to Kalshi via REST.
// Implements OrderEmitter — sits as the inner emitter behind QuotaGuard.
//
// Safety:
//   - IOC by default (no resting orders)
//   - Hard cap on contracts per order
//   - Per-order context timeout
//   - All submissions logged with order_id, fill info
//   - Never blocks — errors logged, not propagated to strategies
type KalshiOrderEmitter struct {
	client *kalshiclient.Client
	db     *store.DB
	cfg    RealOrderConfig
	log    *slog.Logger
}

func NewKalshiOrderEmitter(client *kalshiclient.Client, db *store.DB, cfg RealOrderConfig, log *slog.Logger) *KalshiOrderEmitter {
	if cfg.MaxContracts <= 0 {
		cfg.MaxContracts = 50
	}
	if cfg.TimeInForce == "" {
		cfg.TimeInForce = "immediate_or_cancel"
	}
	if cfg.OrderTimeoutS <= 0 {
		cfg.OrderTimeoutS = 10
	}
	return &KalshiOrderEmitter{client: client, db: db, cfg: cfg, log: log}
}

func (e *KalshiOrderEmitter) EmitOrder(o store.Order) bool {
	if !e.cfg.Enabled {
		return false
	}

	// hard cap — never submit more than MaxContracts
	count := o.SuggestedSize
	if count > float64(e.cfg.MaxContracts) {
		count = float64(e.cfg.MaxContracts)
		e.log.Warn("real: clamped order size to max",
			"market", o.MarketTicker, "requested", o.SuggestedSize, "clamped", count)
	}
	if count <= 0 {
		e.log.Warn("real: skipped zero-size order", "market", o.MarketTicker)
		return false
	}

	// persist order to DB as real before submission
	o.IsReal = true
	o.OrderStatus = "pending"
	orderID, err := e.db.InsertRealOrder(context.Background(), o)
	if err != nil {
		e.log.Error("real: failed to persist order to DB",
			"market", o.MarketTicker, "error", err)
		return false
	}
	o.ID = orderID

	// deduct from liquidity pool (cost = count * price * 100 cents)
	spendCents := int64(count * o.MarketPrice * 100)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	newBalance, err := e.db.DeductLiquidityPool(ctx, spendCents)
	if err != nil {
		e.log.Error("real: failed to deduct liquidity pool",
			"market", o.MarketTicker, "spend_cents", spendCents, "error", err)
		return false
	}

	// format price as fixed-point dollars (Kalshi expects string like "0.6500")
	priceStr := fmt.Sprintf("%.4f", o.MarketPrice)
	countStr := fmt.Sprintf("%.2f", count)

	req := createOrderV2Request{
		Ticker:                  o.MarketTicker,
		Side:                    "bid",
		Count:                   countStr,
		Price:                   priceStr,
		TimeInForce:             e.cfg.TimeInForce,
		SelfTradePreventionType: "taker_at_cross",
	}

	var resp createOrderV2Response
	err = e.client.Post(ctx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: order submission FAILED",
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "bid", "count", countStr, "price", priceStr,
			"error", err)
		// refund liquidity pool — order never executed
		if _, refundErr := e.db.RefundLiquidityPool(context.Background(), spendCents); refundErr != nil {
			e.log.Error("real: failed to refund liquidity pool",
				"market", o.MarketTicker, "spend_cents", spendCents, "error", refundErr)
		}
		// mark order as failed in DB
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID); dbErr != nil {
			e.log.Error("real: failed to mark order as failed", "error", dbErr)
		}
		return false
	}

	fillCount, _ := strconv.ParseFloat(resp.FillCount, 64)
	status := "submitted"
	if resp.RemainingCount == "0" && fillCount > 0 {
		status = "filled"
	} else if fillCount > 0 {
		status = "partial"
	}

	if err := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, status); err != nil {
		e.log.Error("real: failed to update order in DB",
			"order_id", resp.OrderID, "error", err)
	}

	e.log.Info("real: order submitted",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"order_id", resp.OrderID,
		"fill_count", resp.FillCount,
		"remaining_count", resp.RemainingCount,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment,
		"pool_balance_cents", newBalance)

	return true
}

var _ OrderEmitter = (*KalshiOrderEmitter)(nil)
