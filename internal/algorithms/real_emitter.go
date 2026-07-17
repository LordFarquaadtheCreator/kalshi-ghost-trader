package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// RealOrderConfig controls real order submission to Kalshi.
type RealOrderConfig struct {
	Enabled       bool
	MaxContracts  int     // hard cap on contracts per order
	Environment   string  // "demo" or "prod" — logged for safety
	TimeInForce   string  // "immediate_or_cancel" or "good_till_canceled"
	OrderTimeoutS int     // per-order HTTP timeout
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
	cfg    RealOrderConfig
	log    *slog.Logger
}

func NewKalshiOrderEmitter(client *kalshiclient.Client, cfg RealOrderConfig, log *slog.Logger) *KalshiOrderEmitter {
	if cfg.MaxContracts <= 0 {
		cfg.MaxContracts = 50
	}
	if cfg.TimeInForce == "" {
		cfg.TimeInForce = "immediate_or_cancel"
	}
	if cfg.OrderTimeoutS <= 0 {
		cfg.OrderTimeoutS = 10
	}
	return &KalshiOrderEmitter{client: client, cfg: cfg, log: log}
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	var resp createOrderV2Response
	err := e.client.Post(ctx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: order submission FAILED",
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "bid", "count", countStr, "price", priceStr,
			"error", err)
		return false
	}

	e.log.Info("real: order submitted",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"order_id", resp.OrderID,
		"fill_count", resp.FillCount,
		"remaining_count", resp.RemainingCount,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment)

	return true
}

var _ OrderEmitter = (*KalshiOrderEmitter)(nil)
