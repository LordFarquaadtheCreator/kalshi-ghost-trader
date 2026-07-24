package kalshiclient

import (
	"context"
	"fmt"
	"math"
	"strconv"
)

// OrderData is the Kalshi order representation from GET /portfolio/orders/{id}.
type OrderData struct {
	OrderID              string `json:"order_id"`
	Ticker               string `json:"ticker"`
	Status               string `json:"status"` // resting, canceled, executed
	FillCountFP          string `json:"fill_count_fp"`
	RemainingCountFP     string `json:"remaining_count_fp"`
	InitialCountFP       string `json:"initial_count_fp"`
	YesPriceDollars      string `json:"yes_price_dollars"`
	NoPriceDollars       string `json:"no_price_dollars"`
	TakerFeesDollars     string `json:"taker_fees_dollars"`
	MakerFeesDollars     string `json:"maker_fees_dollars"`
	TakerFillCostDollars string `json:"taker_fill_cost_dollars"`
	MakerFillCostDollars string `json:"maker_fill_cost_dollars"`
}

// GetOrder fetches a single order by its Kalshi order ID.
// GET /portfolio/orders/{order_id}
func (c *Client) GetOrder(ctx context.Context, orderID string) (*OrderData, error) {
	if orderID == "" {
		return nil, fmt.Errorf("order ID is required")
	}
	var resp struct {
		Order OrderData `json:"order"`
	}
	if err := c.get(ctx, "/portfolio/orders/"+orderID, nil, &resp); err != nil {
		return nil, fmt.Errorf("get order %s: %w", orderID, err)
	}
	return &resp.Order, nil
}

// FillPrice returns the all-in per-contract fill price from a fetched
// OrderData: (|taker_fill_cost_dollars| + |taker_fees_dollars|) / fill_count.
// This is what we actually paid per contract (fill + fees). Used as the cost
// basis for pool reconciliation and P&L.
//
// Falls back to yes_price_dollars (for YES orders) or 1 - yes_price_dollars
// (for NO orders) when taker_fill_cost is missing or zero. Returns 0 when no
// reliable fill price can be derived (zero fill, empty fields, parse errors).
//
// isNO selects the fallback side: true for buy_no / sell-NO orders, false for
// buy_yes / sell-YES. The taker_fill_cost path is side-agnostic — it's the
// absolute cost/proceeds per fill regardless of YES/NO direction.
//
// Note: all-in price can exceed 1.0 for NO orders when fees push the total
// above the NO contract's $1 max payout. We don't clamp — the math needs the
// real cost. Callers that need a [0,1] price (e.g. position avg entry) should
// clamp separately if required.
func (od *OrderData) FillPrice(isNO bool) float64 {
	fc, _ := strconv.ParseFloat(od.FillCountFP, 64)
	if fc > 0 {
		cost, _ := strconv.ParseFloat(od.TakerFillCostDollars, 64)
		fees, _ := strconv.ParseFloat(od.TakerFeesDollars, 64)
		total := math.Abs(cost) + math.Abs(fees)
		if total > 0 {
			return total / fc
		}
	}
	// Fallback: derive from yes_price_dollars. NO price = 1 - yes_price.
	if yp, _ := strconv.ParseFloat(od.YesPriceDollars, 64); yp > 0 {
		if isNO {
			return 1 - yp
		}
		return yp
	}
	return 0
}
