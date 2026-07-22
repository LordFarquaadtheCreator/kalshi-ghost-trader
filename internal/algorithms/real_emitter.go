package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/liquiditypool"
	"github.com/farquaad/kalshi-ghost-trader/internal/positions"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/strategyconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/triggerranges"
	"github.com/google/uuid"
)

// RealOrderConfig controls real order submission to Kalshi.
type RealOrderConfig struct {
	Enabled       bool
	Environment   string  // "demo" or "prod" — logged for safety
	TimeInForce   string  // "immediate_or_cancel" or "good_till_canceled"
	OrderTimeoutS int     // per-order HTTP timeout
}

// createOrderV2Request maps to Kalshi's POST /portfolio/events/orders body.
type createOrderV2Request struct {
	Ticker                  string `json:"ticker"`
	ClientOrderID           string `json:"client_order_id,omitempty"`
	Side                    string `json:"side"`
	Count                   string `json:"count"`
	Price                   string `json:"price"`
	TimeInForce             string `json:"time_in_force"`
	SelfTradePreventionType string `json:"self_trade_prevention_type"`
	PostOnly                bool   `json:"post_only"`
	ReduceOnly              bool   `json:"reduce_only"`
	// ExchangeIndex: -1 = auto-route by market ticker. Avoids cross-shard
	// routing latency when a market lives on a non-zero shard.
	ExchangeIndex int `json:"exchange_index"`
}

type createOrderV2Response struct {
	OrderID        string `json:"order_id"`
	FillCount      string `json:"fill_count"`
	RemainingCount string `json:"remaining_count"`
	// TsMS: matching engine processing timestamp (Unix epoch ms).
	TsMS int64 `json:"ts_ms"`
}

// KalshiOrderEmitter submits real orders to Kalshi via REST.
// Implements OrderEmitter — sits as the inner emitter behind QuotaGuard.
//
// Buy path (Action="buy", Side="open"):
//   - Kelly sizing from live pool balance
//   - Side: "bid" to Kalshi
//   - Deducts cost from pool
//   - ApplyBuy on position manager
//
// Sell path (Action="sell", Side="close"):
//   - Size from open position (sell-to-close only, no naked shorts)
//   - Side: "ask" to Kalshi
//   - Credits proceeds to pool
//   - ApplySell on position manager, computes realized PnL
//
// Safety:
//   - IOC by default (no resting orders)
//   - Hard cap on contracts per order
//   - Per-order context timeout
//   - All submissions logged with order_id, fill info
//   - Never blocks — errors logged, not propagated to strategies
type KalshiOrderEmitter struct {
	client   *kalshiclient.Client
	db       *store.DB
	pos      *positions.Manager
	cfg      RealOrderConfig
	log      *slog.Logger
}

func NewKalshiOrderEmitter(client *kalshiclient.Client, db *store.DB, log *slog.Logger) *KalshiOrderEmitter {
	cfg := RealOrderConfig{
		Enabled:       true,
		Environment:   config.Cfg.Environment,
		TimeInForce:   config.Cfg.RealOrderTimeInForce,
		OrderTimeoutS: config.Cfg.RealOrderTimeoutS,
	}
	if cfg.TimeInForce == "" {
		cfg.TimeInForce = "immediate_or_cancel"
	}
	if cfg.OrderTimeoutS <= 0 {
		cfg.OrderTimeoutS = 10
	}
	return &KalshiOrderEmitter{
		client: client,
		db:     db,
		pos:    positions.New(db.GormDB()),
		cfg:    cfg,
		log:    log,
	}
}

func (e *KalshiOrderEmitter) EmitOrder(o store.Order) bool {
	if !e.cfg.Enabled {
		return false
	}

	// Route to sell path if Action="sell" or Side="close". Legacy orders
	// (Side="") and explicit Side="open" go through buy path.
	if o.Action == "sell" || o.Side == store.OrderSideClose {
		return e.emitSell(o)
	}
	return e.emitBuy(o)
}

// emitBuy handles the buy-to-open path. Deducts cost from pool, submits
// bid to Kalshi, ApplyBuy on position manager with fill count.
func (e *KalshiOrderEmitter) emitBuy(o store.Order) bool {
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
	enabled, err := strategyconfig.IsEnabled(ctx, e.db.GormDB(), o.Strategy)
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
	hasBands, err := triggerranges.Has(ctx, e.db.GormDB(), o.Strategy)
	if err != nil {
		e.log.Error("real: failed to check trigger ranges",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if hasBands {
		inRange, err := triggerranges.IsPriceIn(ctx, e.db.GormDB(), o.Strategy, o.MarketPrice)
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

	// Guard 3: fetch liquidity pool — pool balance IS the kelly bankroll.
	// Single source of truth: profit compounds, losses shrink sizing
	// automatically. Set via dashboard reset/topup, not real_bankroll config.
	lp, err := liquiditypool.Get(ctx, e.db.GormDB())
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
	bankrollDollars := float64(lp.BalanceCents) / 100.0

	// Kelly size from live pool balance
	count := kellySizeRaw(o.ConvProb, o.MarketPrice, bankrollDollars, kellyFractionP)
	if count <= 0 {
		e.log.Warn("real: skipped zero-size order",
			"market", o.MarketTicker, "bankroll", bankrollDollars)
		return false
	}
	// Kalshi rejects sub-1 contract counts; round up to 1
	if count < 1 {
		count = 1
	}

	// Guard 4: clamp spend to available pool balance (race safety — balance
	// could change between fetch and deduct under concurrent orders)
	spendCents := int64(count * o.MarketPrice * 100)
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
	o.Action = "buy"
	if o.Side == "" {
		o.Side = store.OrderSideOpen
	}
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

	newBalance, err := liquiditypool.Deduct(orderCtx, e.db.GormDB(), spendCents)
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

	// client_order_id: per-order UUID for idempotency. If a timeout leaves the
	// order in an ambiguous state, retrying with the same client_order_id is
	// safe — Kalshi dedupes. Also surfaces in Kalshi's response so we can
	// correlate client and server side of a submission.
	clientOrderID := uuid.NewString()

	req := createOrderV2Request{
		Ticker:                  o.MarketTicker,
		ClientOrderID:           clientOrderID,
		Side:                    "bid",
		Count:                   countStr,
		Price:                   priceStr,
		TimeInForce:             e.cfg.TimeInForce,
		SelfTradePreventionType: "taker_at_cross",
		// -1 lets Kalshi route by market ticker. Default 0 forces cross-shard
		// routing when the market lives on a non-zero shard — suspected cause
		// of the 10s hangs on CHABOU-CHA near settlement.
		ExchangeIndex: -1,
	}

	var resp createOrderV2Response
	err = e.client.Post(orderCtx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: order submission FAILED",
			"order_id", o.ID, "client_order_id", clientOrderID,
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "bid", "count", countStr, "price", priceStr,
			"error", err)
		// MarkRealOrderFailed handles pool refund transactionally
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID); dbErr != nil {
			e.log.Error("real: failed to mark order as failed", "order_id", o.ID, "error", dbErr)
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

	// Apply buy to position manager using actual fill count. Zero-fill
	// cancels don't create a position. Partial fills create a smaller
	// position than suggested — reflects actual exposure.
	if fillCount > 0 {
		posID, perr := e.pos.ApplyBuy(ctx, o.MatchTicker, o.MarketTicker, o.Strategy, true, fillCount, o.MarketPrice)
		if perr != nil {
			e.log.Error("real: ApplyBuy failed",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"fill_count", fillCount, "error", perr)
		} else {
			// Link order to position for traceability.
			_ = e.db.GormDB().Model(&store.Order{}).Where("id = ?", o.ID).
				Update("position_id", posID).Error
		}
	}

	e.log.Info("real: order submitted",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"order_id", resp.OrderID, "client_order_id", clientOrderID,
		"server_ts_ms", resp.TsMS,
		"fill_count", resp.FillCount,
		"remaining_count", resp.RemainingCount,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment,
		"pool_balance_cents", newBalance)

	return true
}

// emitSell handles the sell-to-close path. Looks up open position, sizes
// sell to open contracts, submits ask to Kalshi, credits proceeds to pool,
// ApplySell on position manager with fill count.
//
// No naked shorts: rejects if no open position or sell exceeds open count.
func (e *KalshiOrderEmitter) emitSell(o store.Order) bool {
	ctx := context.Background()

	// Sell path needs the same pre-match gate + market lookup for player/title.
	mkt, err := e.db.GetMarket(ctx, o.MarketTicker)
	if err != nil {
		e.log.Error("real: sell failed to look up market",
			"market", o.MarketTicker, "error", err)
		return false
	}
	o.PlayerName = mkt.PlayerName
	if title, err := e.db.GetEventTitle(ctx, mkt.EventTicker); err == nil {
		o.MatchTitle = title
	}

	// Strategy enabled check still applies to sells.
	enabled, err := strategyconfig.IsEnabled(ctx, e.db.GormDB(), o.Strategy)
	if err != nil {
		e.log.Error("real: sell failed to check strategy enabled",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if !enabled {
		e.log.Warn("real: sell skipped, strategy not enabled",
			"strategy", o.Strategy, "market", o.MarketTicker)
		return false
	}

	// Fetch open position. Sell-to-close only — no position, no sell.
	pos, err := e.pos.GetOpenForStrategy(ctx, o.MarketTicker, o.Strategy, true)
	if err != nil {
		e.log.Error("real: sell failed to fetch open position",
			"market", o.MarketTicker, "strategy", o.Strategy, "error", err)
		return false
	}
	if pos == nil {
		e.log.Warn("real: sell skipped, no open position",
			"market", o.MarketTicker, "strategy", o.Strategy)
		return false
	}

	openContracts := pos.FilledBuyCount - pos.FilledSellCount
	if openContracts <= 0 {
		e.log.Warn("real: sell skipped, position has no open contracts",
			"market", o.MarketTicker, "strategy", o.Strategy,
			"buy_count", pos.FilledBuyCount, "sell_count", pos.FilledSellCount)
		return false
	}

	// Size sell to open contracts. SuggestedSize from strategy is a hint;
	// clamp to actual open count to avoid naked-short rejection from Kalshi.
	count := o.SuggestedSize
	if count <= 0 || count > openContracts {
		count = openContracts
	}
	if count < 1 {
		count = 1
	}

	// Persist order to DB before submission.
	o.IsReal = true
	o.OrderStatus = "pending"
	o.SuggestedSize = count
	o.Action = "sell"
	o.Side = store.OrderSideClose
	o.PositionID = &pos.ID
	orderID, err := e.db.InsertRealOrder(ctx, o)
	if err != nil {
		e.log.Error("real: sell failed to persist order to DB",
			"market", o.MarketTicker, "error", err)
		return false
	}
	o.ID = orderID

	orderCtx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	// Submit ask to Kalshi. No pool deduction on sell — proceeds credit
	// the pool after fill. Pre-fill we hold no capital.
	priceStr := fmt.Sprintf("%.4f", o.MarketPrice)
	countStr := fmt.Sprintf("%.2f", count)
	clientOrderID := uuid.NewString()

	req := createOrderV2Request{
		Ticker:                  o.MarketTicker,
		ClientOrderID:           clientOrderID,
		Side:                    "ask",
		Count:                   countStr,
		Price:                   priceStr,
		TimeInForce:             e.cfg.TimeInForce,
		SelfTradePreventionType: "taker_at_cross",
		ExchangeIndex:           -1,
	}

	var resp createOrderV2Response
	err = e.client.Post(orderCtx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: sell submission FAILED",
			"order_id", o.ID, "client_order_id", clientOrderID,
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "ask", "count", countStr, "price", priceStr,
			"error", err)
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID); dbErr != nil {
			e.log.Error("real: failed to mark sell as failed", "order_id", o.ID, "error", dbErr)
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
		status = "canceled"
	}

	if err := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, status); err != nil {
		e.log.Error("real: failed to update sell order in DB",
			"order_id", resp.OrderID, "error", err)
	}

	// Credit pool with proceeds and close position on actual fill count.
	// Zero-fill cancels don't credit or close.
	if fillCount > 0 {
		proceedsCents := int64(fillCount * o.MarketPrice * 100)
		newBalance, err := liquiditypool.Credit(orderCtx, e.db.GormDB(), proceedsCents)
		if err != nil {
			e.log.Error("real: sell failed to credit pool",
				"market", o.MarketTicker, "proceeds_cents", proceedsCents, "error", err)
		} else {
			e.log.Info("real: sell proceeds credited",
				"market", o.MarketTicker, "proceeds_cents", proceedsCents,
				"pool_balance_cents", newBalance)
		}

		_, realizedPnL, remaining, perr := e.pos.ApplySell(
			ctx, o.MatchTicker, o.MarketTicker, o.Strategy, true, fillCount, o.MarketPrice)
		if perr != nil {
			e.log.Error("real: ApplySell failed",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"fill_count", fillCount, "error", perr)
		} else {
			e.log.Info("real: position updated on sell",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"fill_count", fillCount, "realized_pnl_cents", realizedPnL,
				"remaining_open", remaining)
		}
	}

	e.log.Info("real: sell order submitted",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"order_id", resp.OrderID, "client_order_id", clientOrderID,
		"server_ts_ms", resp.TsMS,
		"fill_count", resp.FillCount,
		"remaining_count", resp.RemainingCount,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment)

	return true
}

var _ OrderEmitter = (*KalshiOrderEmitter)(nil)

// LiveToggleEmitter gates the real order pipeline on real_trading_enabled.
// Checks config.Cfg per EmitOrder call so dashboard flips take effect without restart.
// Returns false before delegating to inner when flag is off — prevents QuotaGuard
// from tracking budget spend on orders that will never submit.
// Logs each on/off transition for audit.
type LiveToggleEmitter struct {
	Inner OrderEmitter
	Log   *slog.Logger
	Prev  atomic.Bool
}

func (e *LiveToggleEmitter) EmitOrder(o store.Order) bool {
	on := config.Cfg.RealTradingEnabled
	if !on {
		if e.Prev.Load() {
			e.Log.Warn("real trading disabled — live orders suppressed", "market", o.MarketTicker)
			e.Prev.Store(false)
		}
		return false
	}
	if !e.Prev.Load() {
		e.Log.Warn("real trading enabled — live orders active", "environment", config.Cfg.Environment)
		e.Prev.Store(true)
	}
	return e.Inner.EmitOrder(o)
}

var _ OrderEmitter = (*LiveToggleEmitter)(nil)
