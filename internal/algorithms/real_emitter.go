package algorithms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
	"gorm.io/gorm"
)

// sizeRealOrder converts a raw Kelly float size into a whole-contract count
// and the exact integer-cent spend, clamped to the available pool balance.
//
// kellyRaw is the float output of kellySizeRaw (fraction * edge * bankroll).
// priceCents is the per-contract price in cents (1..99). balanceCents is the
// pool balance in cents.
//
// Returns contracts=0 when kellyRaw floors to 0, when the edge is non-positive,
// or when the pool cannot afford a single contract at priceCents. The minimum
// of one contract is applied exactly once — after the clamp, not before — so
// a clamp that brings the count below 1 results in no order rather than a
// rounded-up order the pool can't cover.
func sizeRealOrder(kellyRaw float64, priceCents, balanceCents int64) (contracts int, spendCents int64) {
	if priceCents <= 0 || balanceCents <= 0 {
		return 0, 0
	}
	contracts = int(math.Floor(kellyRaw))
	if contracts < 1 {
		return 0, 0
	}
	spendCents = int64(contracts) * priceCents
	if spendCents > balanceCents {
		// Clamp to what the pool can afford. Integer division floors, which
		// is correct — we cannot buy a fractional contract.
		maxContracts := balanceCents / priceCents
		if maxContracts < 1 {
			return 0, 0
		}
		contracts = int(maxContracts)
		spendCents = int64(contracts) * priceCents
	}
	return contracts, spendCents
}

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
	// buy_no = buy NO contracts (long NO). Kalshi V2: side="ask", reduce_only=false.
	if o.Action == "buy_no" {
		return e.emitBuyNO(o)
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
		started, err := e.db.GetKalshiScore(ctx, mkt.EventTicker)
		liveStarted := false
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				e.log.Error("real: failed to look up kalshi score",
					"market", o.MarketTicker, "error", err)
			}
			// not-found or error: no snapshot yet, fall through to HasPoints
		} else {
			liveStarted = started.Status == "started"
		}
		if !liveStarted {
			hasPts, perr := e.db.HasPoints(ctx, mkt.EventTicker)
			if perr != nil {
				e.log.Error("real: failed to check points",
					"market", o.MarketTicker, "error", perr)
			}
			liveStarted = hasPts
		}
		if !liveStarted {
			e.log.Warn("real: match not started yet, skipping",
				"market", o.MarketTicker, "occurrence_ts", mkt.OccurrenceTS)
			return false
		}
		e.log.Info("real: occurrence_ts in future but match live, proceeding",
			"market", o.MarketTicker,
			"occurrence_ts", mkt.OccurrenceTS,
			"kalshi_status", started.Status,
			"has_points", liveStarted)
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

	// Guard 1b: per-market strategy limit — caps how many real buy orders a
	// strategy may place on the same market. 0 = no limit. Sells bypass.
	maxOrders, err := strategyconfig.GetLimit(ctx, e.db.GormDB(), o.Strategy)
	if err != nil {
		e.log.Error("real: failed to check per-market strategy limit",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if maxOrders > 0 {
		count, err := e.db.CountRealOrdersByMarketStrategy(ctx, o.MarketTicker, o.Strategy)
		if err != nil {
			e.log.Error("real: failed to count real orders for per-market limit",
				"market", o.MarketTicker, "strategy", o.Strategy, "error", err)
			return false
		}
		if count >= int64(maxOrders) {
			e.log.Info("real: per-market strategy limit reached, skipping",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"count", count, "limit", maxOrders)
			return false
		}
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

	// Kelly size from live pool balance.
	// sizeRealOrder floors to a whole contract count and clamps spend to
	// the available pool balance. Returns 0 contracts when Kelly says 0,
	// when the edge is non-positive, or when the pool can't afford one
	// contract at the current price.
	priceCents := int64(math.Round(o.MarketPrice * 100))
	contracts, spendCents := sizeRealOrder(
		kellySizeRaw(o.ConvProb, o.MarketPrice, bankrollDollars, kellyFractionP),
		priceCents, lp.BalanceCents)
	if contracts < 1 {
		e.log.Warn("real: skipped zero-size order",
			"market", o.MarketTicker,
			"bankroll", bankrollDollars,
			"price_cents", priceCents,
			"balance_cents", lp.BalanceCents,
			"contracts", contracts)
		return false
	}

	// persist order to DB as real before submission
	o.IsReal = true
	o.OrderStatus = "pending"
	o.SuggestedSize = float64(contracts)
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
		// No refund — Deduct failed, pool was never debited.
		if dbErr := e.db.MarkRealOrderFailedNoRefund(context.Background(), o.ID, "pool_deduct_failed: "+err.Error()); dbErr != nil {
			e.log.Error("real: failed to mark order as failed after pool deduction error", "error", dbErr)
		}
		return false
	}

	// Submit IOC bid at signal price. On zero-fill, retry as market order.
	clientOrderID := uuid.NewString()
	priceStr := fmt.Sprintf("%.4f", o.MarketPrice)
	fillCount, zeroFill, submitted := e.submitBuyToKalshi(orderCtx, o, contracts, priceStr, clientOrderID, "ioc_zero_fill", newBalance)
	if !submitted {
		return false
	}
	if zeroFill {
		fillCount = e.retryBuyAsMarket(ctx, o, contracts)
	}

	// Paired orders (cross-arb): return false on zero-fill so strategy
	// skips leg 2. Without this, leg 1 zero-fills but returns true,
	// strategy emits leg 2, and we get naked directional risk.
	if o.PairID != "" && fillCount == 0 {
		return false
	}
	return true
}

// submitBuyToKalshi posts a buy bid to Kalshi and processes the response.
// Handles fill detection, fill-price fetch, DB update, position application,
// and logging. Shared by emitBuy's initial attempt and the market-order retry.
//
// zeroFillReason is used as the cancel reason when IOC zero-fills.
// poolBalanceCents is included in the success log for audit.
//
// Returns (fillCount, zeroFill, submitted). submitted=false only on POST
// failure — caller should return false. zeroFill=true when IOC canceled with
// no fills — caller may retry as market order.
func (e *KalshiOrderEmitter) submitBuyToKalshi(
	orderCtx context.Context,
	o store.Order,
	contracts int,
	priceStr string,
	clientOrderID string,
	zeroFillReason string,
	poolBalanceCents int64,
) (fillCount float64, zeroFill bool, submitted bool) {
	countStr := strconv.Itoa(contracts)

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
	err := e.client.Post(orderCtx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: order submission FAILED",
			"order_id", o.ID, "client_order_id", clientOrderID,
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "bid", "count", countStr, "price", priceStr,
			"error", err)
		// MarkRealOrderFailed handles pool refund transactionally
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID, "submit_failed: "+err.Error()); dbErr != nil {
			e.log.Error("real: failed to mark order as failed", "order_id", o.ID, "error", dbErr)
		}
		return 0, false, false
	}

	fillCount, err = strconv.ParseFloat(resp.FillCount, 64)
	if err != nil {
		e.log.Error("real: unparseable fill_count, marking unverified",
			"order_id", o.ID, "server_order_id", resp.OrderID,
			"fill_count_raw", resp.FillCount, "remaining_count_raw", resp.RemainingCount,
			"error", err)
		if uerr := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, 0, 0, "unverified", "unparseable_fill_count: "+resp.FillCount); uerr != nil {
			e.log.Error("real: failed to mark order unverified", "order_id", o.ID, "error", uerr)
		}
		return 0, false, true
	}
	remainingCount, err := strconv.ParseFloat(resp.RemainingCount, 64)
	if err != nil {
		e.log.Error("real: unparseable remaining_count, marking unverified",
			"order_id", o.ID, "server_order_id", resp.OrderID,
			"fill_count_raw", resp.FillCount, "remaining_count_raw", resp.RemainingCount,
			"error", err)
		if uerr := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, 0, "unverified", "unparseable_remaining_count: "+resp.RemainingCount); uerr != nil {
			e.log.Error("real: failed to mark order unverified", "order_id", o.ID, "error", uerr)
		}
		return fillCount, false, true
	}
	status := "submitted"
	cancelReason := ""
	if remainingCount == 0 && fillCount > 0 {
		status = "filled"
	} else if fillCount > 0 {
		status = "partial"
	} else if remainingCount == 0 {
		// IOC with zero fill — fully canceled by Kalshi
		status = "canceled"
		cancelReason = zeroFillReason
	}

	// Fetch actual fill price from Kalshi for filled/partial orders. Used
	// for pool reconciliation (signal price vs actual cost) and position
	// avg-entry. Zero-fill cancels and unverified orders skip this — no
	// fill to price.
	fillPrice := 0.0
	if fillCount > 0 {
		fillPrice = e.fetchFillPrice(orderCtx, resp.OrderID, false)
	}

	if err := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, fillPrice, status, cancelReason); err != nil {
		e.log.Error("real: failed to update order in DB",
			"order_id", resp.OrderID, "error", err)
	}

	// Apply buy to position manager using actual fill count. Zero-fill
	// cancels don't create a position. Partial fills create a smaller
	// position than suggested — reflects actual exposure.
	// Use fill_price for avg entry when available; fall back to market_price.
	if fillCount > 0 {
		entryPrice := fillPrice
		if entryPrice <= 0 {
			entryPrice = o.MarketPrice
		}
		posID, perr := e.pos.ApplyBuy(context.Background(), o.MatchTicker, o.MarketTicker, o.Strategy, true, fillCount, entryPrice)
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
		"fill_price", fillPrice,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment,
		"pool_balance_cents", poolBalanceCents)

	zeroFill = (status == "canceled" && cancelReason != "")
	return fillCount, zeroFill, true
}

// retryBuyAsMarket submits a second IOC bid at a marketable price after the
// original IOC zero-filled. The original order was already marked canceled and
// the pool refunded by UpdateRealOrder. Creates a fresh order row, re-deducts
// the pool at the market bid price, and submits a bid high enough to cross any
// ask (capped at what the pool can afford).
//
// Bidding at 0.99 fills at best ask — actual fill price reflects real
// liquidity, not our bid. reconcileFillPrice handles the gap between the
// deduction at bid price and the actual fill cost.
//
// Returns the fill count from the retry (0 if retry was skipped or failed).
func (e *KalshiOrderEmitter) retryBuyAsMarket(
	ctx context.Context,
	o store.Order,
	contracts int,
) float64 {
	// Fresh timeout — original orderCtx may be nearly exhausted.
	orderCtx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	// Pool was refunded by UpdateRealOrder on the zero-fill cancel.
	lp, err := liquiditypool.Get(ctx, e.db.GormDB())
	if err != nil {
		e.log.Error("real: market retry failed to get pool balance",
			"market", o.MarketTicker, "error", err)
		return 0
	}
	if lp.BalanceCents <= 0 {
		e.log.Warn("real: market retry skipped, pool empty",
			"market", o.MarketTicker)
		return 0
	}

	// Bid high enough to cross any ask. Cap at what pool can afford.
	// At 0.99 bid, matching engine fills at best ask price(s), not 0.99.
	// Pool reconciled on actual fill — over-deduction refunded.
	maxPrice := math.Min(0.99, float64(lp.BalanceCents)/float64(contracts)/100.0)
	if maxPrice < 0.01 {
		e.log.Warn("real: market retry skipped, pool too small for one contract",
			"market", o.MarketTicker, "balance_cents", lp.BalanceCents, "contracts", contracts)
		return 0
	}

	retryPriceCents := int64(math.Floor(maxPrice * 100))
	if retryPriceCents < 1 {
		return 0
	}
	retryPrice := float64(retryPriceCents) / 100.0
	retrySpendCents := int64(contracts) * retryPriceCents

	// Fresh order row for the retry — original stays as canceled.
	o.ID = 0
	o.OrderStatus = "pending"
	o.MarketPrice = retryPrice // so refund/reconcile uses retry price
	retryOrderID, err := e.db.InsertRealOrder(ctx, o)
	if err != nil {
		e.log.Error("real: market retry failed to persist order",
			"market", o.MarketTicker, "error", err)
		return 0
	}
	o.ID = retryOrderID

	newBalance, err := liquiditypool.Deduct(orderCtx, e.db.GormDB(), retrySpendCents)
	if err != nil {
		e.log.Error("real: market retry failed to deduct pool",
			"market", o.MarketTicker, "spend_cents", retrySpendCents, "error", err)
		if dbErr := e.db.MarkRealOrderFailedNoRefund(context.Background(), o.ID, "market_retry_pool_deduct_failed: "+err.Error()); dbErr != nil {
			e.log.Error("real: market retry failed to mark order failed", "error", dbErr)
		}
		return 0
	}

	e.log.Info("real: retrying as market order",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"contracts", contracts, "bid_price", retryPrice,
		"pool_balance_cents", newBalance)

	clientOrderID := uuid.NewString()
	priceStr := fmt.Sprintf("%.4f", retryPrice)
	fillCount, _, _ := e.submitBuyToKalshi(orderCtx, o, contracts, priceStr, clientOrderID, "market_retry_zero_fill", newBalance)
	return fillCount
}

// emitBuyNO handles the buy-NO-to-open path. Buys NO contracts (long NO)
// via Kalshi V2 side="ask" with reduce_only=false. Deducts NO cost from
// pool, ApplyBuy on position manager with fill count.
//
// Used by cross-arb NO arb path: when yesSum > 1.0, buy NO on both markets.
// One NO always wins → guaranteed profit = yesSum - 1.0.
func (e *KalshiOrderEmitter) emitBuyNO(o store.Order) bool {
	ctx := context.Background()

	// Same pre-match gate as emitBuy.
	mkt, err := e.db.GetMarket(ctx, o.MarketTicker)
	if err != nil {
		e.log.Error("real: buy_no failed to look up market",
			"market", o.MarketTicker, "error", err)
		return false
	}
	if mkt.OccurrenceTS > 0 && time.Now().UnixMilli() < mkt.OccurrenceTS {
		started, err := e.db.GetKalshiScore(ctx, mkt.EventTicker)
		liveStarted := false
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				e.log.Error("real: buy_no failed to look up kalshi score",
					"market", o.MarketTicker, "error", err)
			}
		} else {
			liveStarted = started.Status == "started"
		}
		if !liveStarted {
			hasPts, perr := e.db.HasPoints(ctx, mkt.EventTicker)
			if perr != nil {
				e.log.Error("real: buy_no failed to check points",
					"market", o.MarketTicker, "error", perr)
			}
			liveStarted = hasPts
		}
		if !liveStarted {
			e.log.Warn("real: buy_no match not started, skipping",
				"market", o.MarketTicker, "occurrence_ts", mkt.OccurrenceTS)
			return false
		}
	}

	o.PlayerName = mkt.PlayerName
	if title, err := e.db.GetEventTitle(ctx, mkt.EventTicker); err == nil {
		o.MatchTitle = title
	}

	// Strategy enabled check.
	enabled, err := strategyconfig.IsEnabled(ctx, e.db.GormDB(), o.Strategy)
	if err != nil {
		e.log.Error("real: buy_no failed to check strategy enabled",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if !enabled {
		e.log.Warn("real: buy_no strategy not enabled, skipping",
			"strategy", o.Strategy, "market", o.MarketTicker)
		return false
	}

	// Per-market strategy limit.
	maxOrders, err := strategyconfig.GetLimit(ctx, e.db.GormDB(), o.Strategy)
	if err != nil {
		e.log.Error("real: buy_no failed to check per-market strategy limit",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if maxOrders > 0 {
		count, err := e.db.CountRealOrdersByMarketStrategy(ctx, o.MarketTicker, o.Strategy)
		if err != nil {
			e.log.Error("real: buy_no failed to count real orders",
				"market", o.MarketTicker, "strategy", o.Strategy, "error", err)
			return false
		}
		if count >= int64(maxOrders) {
			e.log.Info("real: buy_no per-market limit reached, skipping",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"count", count, "limit", maxOrders)
			return false
		}
	}

	// Trigger range check — NO price is o.MarketPrice (already 1-YES).
	hasBands, err := triggerranges.Has(ctx, e.db.GormDB(), o.Strategy)
	if err != nil {
		e.log.Error("real: buy_no failed to check trigger ranges",
			"strategy", o.Strategy, "error", err)
		return false
	}
	if hasBands {
		inRange, err := triggerranges.IsPriceIn(ctx, e.db.GormDB(), o.Strategy, o.MarketPrice)
		if err != nil {
			e.log.Error("real: buy_no failed to check price in trigger range",
				"strategy", o.Strategy, "price", o.MarketPrice, "error", err)
			return false
		}
		if !inRange {
			e.log.Info("real: buy_no price outside trigger ranges, skipping",
				"strategy", o.Strategy, "market", o.MarketTicker, "price", o.MarketPrice)
			return false
		}
	}

	// Pool balance = kelly bankroll.
	lp, err := liquiditypool.Get(ctx, e.db.GormDB())
	if err != nil {
		e.log.Error("real: buy_no failed to get pool balance",
			"market", o.MarketTicker, "error", err)
		return false
	}
	if lp.BalanceCents <= 0 {
		e.log.Warn("real: buy_no pool empty, skipping",
			"market", o.MarketTicker, "balance_cents", lp.BalanceCents)
		return false
	}
	bankrollDollars := float64(lp.BalanceCents) / 100.0

	// Kelly size from live pool. ConvProb for buy_no = probability NO wins.
	priceCents := int64(math.Round(o.MarketPrice * 100))
	contracts, spendCents := sizeRealOrder(
		kellySizeRaw(o.ConvProb, o.MarketPrice, bankrollDollars, kellyFractionP),
		priceCents, lp.BalanceCents)
	if contracts < 1 {
		e.log.Warn("real: buy_no skipped zero-size",
			"market", o.MarketTicker,
			"bankroll", bankrollDollars,
			"price_cents", priceCents,
			"contracts", contracts)
		return false
	}

	// Persist order to DB before submission.
	o.IsReal = true
	o.OrderStatus = "pending"
	o.SuggestedSize = float64(contracts)
	o.Action = "buy_no"
	o.Side = store.OrderSideOpen
	orderID, err := e.db.InsertRealOrder(ctx, o)
	if err != nil {
		e.log.Error("real: buy_no failed to persist order",
			"market", o.MarketTicker, "error", err)
		return false
	}
	o.ID = orderID

	// Deduct NO cost from pool.
	orderCtx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	newBalance, err := liquiditypool.Deduct(orderCtx, e.db.GormDB(), spendCents)
	if err != nil {
		e.log.Error("real: buy_no failed to deduct pool",
			"market", o.MarketTicker, "spend_cents", spendCents, "error", err)
		// No refund — Deduct failed, pool was never debited.
		if dbErr := e.db.MarkRealOrderFailedNoRefund(context.Background(), o.ID, "buy_no_pool_deduct_failed: "+err.Error()); dbErr != nil {
			e.log.Error("real: buy_no failed to mark order failed", "error", dbErr)
		}
		return false
	}

	priceStr := fmt.Sprintf("%.4f", o.MarketPrice)
	countStr := strconv.Itoa(contracts)
	clientOrderID := uuid.NewString()

	// Kalshi V2: side="ask" buys NO (long NO). reduce_only=false opens position.
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
		e.log.Error("real: buy_no submission FAILED",
			"order_id", o.ID, "client_order_id", clientOrderID,
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "ask", "count", countStr, "price", priceStr,
			"error", err)
		if dbErr := e.db.MarkRealOrderFailed(context.Background(), o.ID, "buy_no_submit_failed: "+err.Error()); dbErr != nil {
			e.log.Error("real: buy_no failed to mark order failed", "error", dbErr)
		}
		return false
	}

	fillCount, err := strconv.ParseFloat(resp.FillCount, 64)
	if err != nil {
		e.log.Error("real: buy_no unparseable fill_count",
			"order_id", o.ID, "fill_count_raw", resp.FillCount, "error", err)
		if uerr := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, 0, 0, "unverified", "buy_no_unparseable_fill_count: "+resp.FillCount); uerr != nil {
			e.log.Error("real: buy_no failed to mark unverified", "error", uerr)
		}
		return true
	}
	remainingCount, err := strconv.ParseFloat(resp.RemainingCount, 64)
	if err != nil {
		e.log.Error("real: buy_no unparseable remaining_count",
			"order_id", o.ID, "remaining_count_raw", resp.RemainingCount, "error", err)
		if uerr := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, 0, "unverified", "buy_no_unparseable_remaining_count: "+resp.RemainingCount); uerr != nil {
			e.log.Error("real: buy_no failed to mark unverified", "error", uerr)
		}
		return true
	}
	status := "submitted"
	cancelReason := ""
	if remainingCount == 0 && fillCount > 0 {
		status = "filled"
	} else if fillCount > 0 {
		status = "partial"
	} else if remainingCount == 0 {
		status = "canceled"
		cancelReason = "buy_no_ioc_zero_fill"
	}

	// Fetch actual fill price (NO side). Used for pool reconciliation + avg entry.
	fillPrice := 0.0
	if fillCount > 0 {
		fillPrice = e.fetchFillPrice(orderCtx, resp.OrderID, true)
	}

	if err := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, fillPrice, status, cancelReason); err != nil {
		e.log.Error("real: buy_no failed to update order in DB",
			"order_id", resp.OrderID, "error", err)
	}

	if fillCount > 0 {
		entryPrice := fillPrice
		if entryPrice <= 0 {
			entryPrice = o.MarketPrice
		}
		posID, perr := e.pos.ApplyBuyNO(ctx, o.MatchTicker, o.MarketTicker, o.Strategy, true, fillCount, entryPrice)
		if perr != nil {
			e.log.Error("real: buy_no ApplyBuyNO failed",
				"market", o.MarketTicker, "strategy", o.Strategy,
				"fill_count", fillCount, "error", perr)
		} else {
			_ = e.db.GormDB().Model(&store.Order{}).Where("id = ?", o.ID).
				Update("position_id", posID).Error
		}
	}

	e.log.Info("real: buy_no order submitted",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"order_id", resp.OrderID, "client_order_id", clientOrderID,
		"server_ts_ms", resp.TsMS,
		"fill_count", resp.FillCount,
		"remaining_count", resp.RemainingCount,
		"fill_price", fillPrice,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment,
		"pool_balance_cents", newBalance)

	// Paired orders: return false on zero-fill so strategy skips leg 2.
	if o.PairID != "" && fillCount == 0 {
		return false
	}
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

	// Submit IOC ask at signal price. On zero-fill, retry as market order.
	clientOrderID := uuid.NewString()
	priceStr := fmt.Sprintf("%.4f", o.MarketPrice)
	_, zeroFill, submitted := e.submitSellToKalshi(orderCtx, o, count, priceStr, clientOrderID, "sell_ioc_zero_fill")
	if !submitted {
		return false
	}
	if zeroFill {
		e.retrySellAsMarket(ctx, o, count)
	}

	return true
}

// submitSellToKalshi posts a sell ask to Kalshi and processes the response.
// Handles fill detection, fill-price fetch, pool credit, DB update, position
// close, and logging. Shared by emitSell's initial attempt and the market-order
// retry.
//
// zeroFillReason is used as the cancel reason when IOC zero-fills.
//
// Returns (fillCount, zeroFill, submitted). submitted=false only on POST
// failure — caller should return false. zeroFill=true when IOC canceled with
// no fills — caller may retry as market order.
func (e *KalshiOrderEmitter) submitSellToKalshi(
	orderCtx context.Context,
	o store.Order,
	count float64,
	priceStr string,
	clientOrderID string,
	zeroFillReason string,
) (fillCount float64, zeroFill bool, submitted bool) {
	countStr := fmt.Sprintf("%.2f", count)

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
	err := e.client.Post(orderCtx, "/portfolio/events/orders", req, &resp)
	if err != nil {
		e.log.Error("real: sell submission FAILED",
			"order_id", o.ID, "client_order_id", clientOrderID,
			"market", o.MarketTicker, "strategy", o.Strategy,
			"side", "ask", "count", countStr, "price", priceStr,
			"error", err)
		// No refund — sells never deduct from pool (they credit on fill).
		if dbErr := e.db.MarkRealOrderFailedNoRefund(context.Background(), o.ID, "sell_submit_failed: "+err.Error()); dbErr != nil {
			e.log.Error("real: failed to mark sell as failed", "order_id", o.ID, "error", dbErr)
		}
		return 0, false, false
	}

	fillCount, err = strconv.ParseFloat(resp.FillCount, 64)
	if err != nil {
		e.log.Error("real: sell unparseable fill_count, marking unverified",
			"order_id", o.ID, "server_order_id", resp.OrderID,
			"fill_count_raw", resp.FillCount, "remaining_count_raw", resp.RemainingCount,
			"error", err)
		if uerr := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, 0, 0, "unverified", "sell_unparseable_fill_count: "+resp.FillCount); uerr != nil {
			e.log.Error("real: failed to mark sell unverified", "order_id", o.ID, "error", uerr)
		}
		return 0, false, true
	}
	remainingCount, err := strconv.ParseFloat(resp.RemainingCount, 64)
	if err != nil {
		e.log.Error("real: sell unparseable remaining_count, marking unverified",
			"order_id", o.ID, "server_order_id", resp.OrderID,
			"fill_count_raw", resp.FillCount, "remaining_count_raw", resp.RemainingCount,
			"error", err)
		if uerr := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, 0, "unverified", "sell_unparseable_remaining_count: "+resp.RemainingCount); uerr != nil {
			e.log.Error("real: failed to mark sell unverified", "order_id", o.ID, "error", uerr)
		}
		return fillCount, false, true
	}
	status := "submitted"
	cancelReason := ""
	if remainingCount == 0 && fillCount > 0 {
		status = "filled"
	} else if fillCount > 0 {
		status = "partial"
	} else if remainingCount == 0 {
		status = "canceled"
		cancelReason = zeroFillReason
	}

	// Fetch actual fill price (we sell YES → isNO=false). Used for pool
	// reconciliation (signal credit vs actual proceeds) and avg exit.
	fillPrice := 0.0
	if fillCount > 0 {
		fillPrice = e.fetchFillPrice(orderCtx, resp.OrderID, false)
	}

	// Credit pool with proceeds at signal price BEFORE UpdateRealOrder so
	// reconcileFillPrice (inside UpdateRealOrder) can adjust for the gap
	// between signal and actual fill price. Zero-fill cancels skip credit.
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
	}

	if err := e.db.UpdateRealOrder(context.Background(), o.ID, resp.OrderID, fillCount, fillPrice, status, cancelReason); err != nil {
		e.log.Error("real: failed to update sell order in DB",
			"order_id", resp.OrderID, "error", err)
	}

	// Close position on actual fill count. Zero-fill cancels don't close.
	if fillCount > 0 {
		exitPrice := fillPrice
		if exitPrice <= 0 {
			exitPrice = o.MarketPrice
		}
		_, realizedPnL, remaining, perr := e.pos.ApplySell(
			context.Background(), o.MatchTicker, o.MarketTicker, o.Strategy, true, fillCount, exitPrice)
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
		"fill_price", fillPrice,
		"count", countStr, "price", priceStr,
		"environment", e.cfg.Environment)

	zeroFill = (status == "canceled" && cancelReason != "")
	return fillCount, zeroFill, true
}

// retrySellAsMarket submits a second IOC ask at a marketable price after the
// original sell IOC zero-filled. Sells never deduct from the pool — they
// credit on fill — so the retry just needs a fresh order row and a low ask
// price that crosses any bid.
//
// Asking at 0.01 fills at best bid — actual fill price reflects real
// liquidity, not our ask. reconcileFillPrice handles the gap between the
// signal credit and actual proceeds.
//
// Returns the fill count from the retry (0 if retry was skipped or failed).
func (e *KalshiOrderEmitter) retrySellAsMarket(
	ctx context.Context,
	o store.Order,
	count float64,
) float64 {
	// Fresh timeout — original orderCtx may be nearly exhausted.
	orderCtx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.OrderTimeoutS)*time.Second)
	defer cancel()

	// Re-check open contracts — position unchanged on zero-fill, but guard
	// against concurrent sells between the original attempt and retry.
	pos, err := e.pos.GetOpenForStrategy(ctx, o.MarketTicker, o.Strategy, true)
	if err != nil || pos == nil {
		e.log.Warn("real: sell market retry skipped, no open position",
			"market", o.MarketTicker, "strategy", o.Strategy, "error", err)
		return 0
	}
	openContracts := pos.FilledBuyCount - pos.FilledSellCount
	if openContracts <= 0 {
		e.log.Warn("real: sell market retry skipped, position closed",
			"market", o.MarketTicker, "strategy", o.Strategy)
		return 0
	}
	if count > openContracts {
		count = openContracts
	}
	if count < 1 {
		count = 1
	}

	// Ask low enough to cross any bid. At 0.01 ask, matching engine fills
	// at best bid price(s), not 0.01. Pool reconciled on actual fill.
	retryPrice := 0.01

	// Fresh order row for the retry — original stays as canceled.
	o.ID = 0
	o.OrderStatus = "pending"
	o.SuggestedSize = count
	o.MarketPrice = retryPrice // so credit/reconcile uses retry price
	o.PositionID = &pos.ID
	retryOrderID, err := e.db.InsertRealOrder(ctx, o)
	if err != nil {
		e.log.Error("real: sell market retry failed to persist order",
			"market", o.MarketTicker, "error", err)
		return 0
	}
	o.ID = retryOrderID

	e.log.Info("real: retrying sell as market order",
		"market", o.MarketTicker, "strategy", o.Strategy,
		"count", count, "ask_price", retryPrice)

	clientOrderID := uuid.NewString()
	priceStr := fmt.Sprintf("%.4f", retryPrice)
	fillCount, _, _ := e.submitSellToKalshi(orderCtx, o, count, priceStr, clientOrderID, "sell_market_retry_zero_fill")
	return fillCount
}

// fetchFillPrice retrieves the actual per-contract fill price from Kalshi
// via GET /portfolio/orders/{order_id}. Called after a successful submit
// when fillCount > 0. Returns 0 on error or when no reliable fill price can
// be derived — caller proceeds with signal-time market_price and pool
// reconciliation is skipped (no fill_price persisted).
//
// isNO selects the YES/NO side for the fallback path (see OrderData.FillPrice).
func (e *KalshiOrderEmitter) fetchFillPrice(ctx context.Context, kalshiOrderID string, isNO bool) float64 {
	if kalshiOrderID == "" {
		return 0
	}
	od, err := e.client.GetOrder(ctx, kalshiOrderID)
	if err != nil {
		e.log.Warn("real: failed to fetch fill price, leaving fill_price=0",
			"kalshi_order_id", kalshiOrderID, "error", err)
		return 0
	}
	fp := od.FillPrice(isNO)
	if fp <= 0 {
		e.log.Warn("real: fill price unavailable from order data",
			"kalshi_order_id", kalshiOrderID,
			"fill_count_fp", od.FillCountFP,
			"taker_fill_cost", od.TakerFillCostDollars,
			"yes_price", od.YesPriceDollars)
	}
	return fp
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
