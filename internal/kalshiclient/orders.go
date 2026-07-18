package kalshiclient

import (
	"context"
	"fmt"
)

// OrderData is the Kalshi order representation from GET /portfolio/orders/{id}.
type OrderData struct {
	OrderID             string `json:"order_id"`
	Ticker              string `json:"ticker"`
	Status              string `json:"status"` // resting, canceled, executed
	FillCountFP         string `json:"fill_count_fp"`
	RemainingCountFP    string `json:"remaining_count_fp"`
	InitialCountFP      string `json:"initial_count_fp"`
	YesPriceDollars     string `json:"yes_price_dollars"`
	NoPriceDollars      string `json:"no_price_dollars"`
	TakerFeesDollars    string `json:"taker_fees_dollars"`
	MakerFeesDollars    string `json:"maker_fees_dollars"`
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
