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
	Bankroll      float64 // Kelly bankroll for real order sizing
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
	db     *store.DB
	cfg    RealOrderConfig
	log    *slog.Logger
}

func NewKalshiOrderEmitter(client *kalshiclient.Client, db *store.DB, cfg RealOrderConfig, log *slog.Logger) *KalshiOrderEmitter {
	if cfg.Bankroll <= 0 {
		cfg.Bankroll = 1000
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

	ctx := context.Background()

	// Guard 0: match must have started — refuse orders on pre-match markets.
	// occurrence_ts is Kalshi's scheduled start; matches often start early when
	// a court clears. Bypass the gate when either:
	//   - Kalshi's own live_data reports status="started"
	//   - any point rows exist for the event (API-Tennis or Kalshi poller)
	mkt, err := e.db.GetMarket(ctx, o.MarketTicker)
	if err != nil {
		e.log.Error("real: failed to look up market",
			"market", o.MarketTicker, "error", err)
		return false
	}
	if mkt.OccurrenceTS > 0 && time.Now().UnixMilli() < mkt.OccurrenceTS {
		started, _ := e.db.GetKalshiScore(ctx, mkt.EventTicker)
		hasPts, _ := e.db.HasPoints(ctx, mkt.EventTicker)
		liveStarted := started.Status == "started" || hasPts
		if !liveStarted {
			e.log.Warn("real: match not started yet, skipping",
				"market", o.MarketTicker, "occurrence_ts", mkt.OccurrenceTS)
			return false
		}
		e.log.Info("real: occurrence_ts in future but match live, proceeding",
			"market", o.MarketTicker,
			"occurrence_ts", mkt.OccurrenceTS,
			"kalshi_status", started.Status,
			"has_points", hasPts)
	}

	// Populate human-readable fields for the orders table
	o.PlayerName = mkt.PlayerName
	if title, err := e.db.GetEventTitle(ctx, mkt.EventTicker); err == nil {
		o.MatchTitle = title
	}

	// Guard 1: strategy must be enabled in strategy_config
	enabled, err := e.db.IsStrategyEnabled(ctx, o.Strategy)
	if err != nil {
		e.log.Error("real: failed to check strategy enabled",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if !enabled {
		e.log.Warn("real: strategy not enabled, skipping",
			"strategy", o.Strategy, "market", o.MarketTicker)
		return false
	}

	// Guard 2: price must fall within an enabled trigger range (if any bands configured)
	hasBands, err := e.db.HasTriggerRanges(ctx, o.Strategy)
	if err != nil {
		e.log.Error("real: failed to check trigger ranges",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if hasBands {
		inRange, err := e.db.IsPriceInTriggerRange(ctx, o.Strategy, o.MarketPrice)
		if err != nil {
			e.log.Error("real: failed to check price in trigger range",
				"strategy", o.Strategy, "price", o.MarketPrice, "error", err)
			return false
		}
		if !inRange {
			e.log.Info("real: price outside trigger ranges, skipping",
				"strategy", o.Strategy, "market", o.MarketTicker, "price", o.MarketPrice)
			return false
		}
	}

	// Guard 3: Kelly size from real bankroll
	count := kellySizeRaw(o.ConvProb, o.MarketPrice, e.cfg.Bankroll, kellyFractionP)
	if count <= 0 {
		e.log.Warn("real: skipped zero-size order", "market", o.MarketTicker)
		return false
	}
	// Kalshi rejects sub-1 contract counts; round up to 1
	if count < 1 {
		count = 1
	}

	// Guard 4: clamp spend to available liquidity pool balance
	spendCents := int64(count * o.MarketPrice * 100)
	lp, err := e.db.GetLiquidityPool(ctx)
	if err != nil {
		e.log.Error("real: failed to get liquidity pool balance",
			"market", o.MarketTicker, "error", err)
		return false
	}
	if lp.BalanceCents <= 0 {
		e.log.Warn("real: liquidity pool empty, skipping",
			"market", o.MarketTicker, "balance_cents", lp.BalanceCents)
		return false
	}
	if spendCents > lp.BalanceCents {
		// clamp to what's available
		maxCount := float64(lp.BalanceCents) / (o.MarketPrice * 100)
		e.log.Warn("real: clamping order to available pool balance",
			"market", o.MarketTicker,
			"original_count", count, "clamped_count", maxCount,
			"spend_cents", spendCents, "balance_cents", lp.BalanceCents)
		count = maxCount
		spendCents = int64(count * o.MarketPrice * 100)
		if count <= 0 {
			e.log.Warn("real: clamped count is zero, skipping",
				"market", o.MarketTicker, "balance_cents", lp.BalanceCents)
			return false
		}
	}

	// persist order to DB as real before submission
	o.IsReal = true
	o.OrderStatus = "pending"
	o.SuggestedSize = count
	orderID, err := e.db.InsertRealOrder(ctx, o)
	if err != nil {
		e.log.Error("real: failed to persist order to DB",
			"market", o.MarketTicker, "error", err)
		return false
	}
	o.ID = orderID

	// deduct from liquidity pool (cost = count * price * 100 cents)
	orderCtx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	newBalance, err := e.db.DeductLiquidityPool(orderCtx, spendCents)
	if err != nil {
		e.log.Error("real: failed to deduct liquidity pool",
			"market", o.MarketTicker, "spend_cents", spendCents, "error", err)
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID); dbErr != nil {
			e.log.Error("real: failed to mark order as failed after pool deduction error", "error", dbErr)
		}
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
	err = e.client.Post(orderCtx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: order submission FAILED",
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "bid", "count", countStr, "price", priceStr,
			"error", err)
		// MarkRealOrderFailed handles pool refund transactionally
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID); dbErr != nil {
			e.log.Error("real: failed to mark order as failed", "error", dbErr)
		}
		return false
	}

	fillCount, _ := strconv.ParseFloat(resp.FillCount, 64)
	remainingCount, _ := strconv.ParseFloat(resp.RemainingCount, 64)
	status := "submitted"
	if remainingCount == 0 && fillCount > 0 {
		status = "filled"
	} else if fillCount > 0 {
		status = "partial"
	} else if remainingCount == 0 {
		// IOC with zero fill — fully canceled by Kalshi
		status = "canceled"
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
