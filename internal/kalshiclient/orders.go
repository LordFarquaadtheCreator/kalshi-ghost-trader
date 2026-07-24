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

// FillPrice returns the actual per-contract fill price from a fetched OrderData.
// Uses |taker_fill_cost_dollars| / fill_count when both are present and
// non-zero (most accurate — reflects the actual matched price including any
// partial fills at multiple levels). Falls back to yes_price_dollars (for YES
// orders) or 1 - yes_price_dollars (for NO orders, derived from the YES price)
// when taker_fill_cost is missing or zero. Returns 0 when no reliable fill
// price can be derived (zero fill, empty fields, parse errors).
//
// isNO selects the fallback side: true for buy_no / sell-NO orders, false for
// buy_yes / sell-YES. The taker_fill_cost path is side-agnostic — it's the
// absolute cost/proceeds per fill regardless of YES/NO direction.
func (od *OrderData) FillPrice(isNO bool) float64 {
	fc, _ := strconv.ParseFloat(od.FillCountFP, 64)
	if fc > 0 {
		if cost, _ := strconv.ParseFloat(od.TakerFillCostDollars, 64); cost != 0 {
			fp := math.Abs(cost) / fc
			if fp > 0 && fp <= 1 {
				return fp
			}
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
