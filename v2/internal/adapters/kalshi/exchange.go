package kalshi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

// Signer provides RSA-PSS-SHA256 auth headers for Kalshi REST requests.
// Implementations live outside this package (ported from v1 kalshiAuth).
type Signer interface {
	AuthHeaders(method, path string) (map[string]string, error)
}

// Exchange implements ports.Exchange via Kalshi REST POST /portfolio/events/orders.
type Exchange struct {
	baseURL   string
	signer    Signer
	http      *http.Client
	timeInForce string
	timeout   time.Duration
}

// NewExchange creates a Kalshi REST exchange adapter.
// signer may be nil for demo/public mode (order submission will fail without auth).
func NewExchange(baseURL string, signer Signer, timeInForce string, timeoutSecs int) *Exchange {
	if timeInForce == "" {
		timeInForce = "immediate_or_cancel"
	}
	if timeoutSecs <= 0 {
		timeoutSecs = 10
	}
	return &Exchange{
		baseURL:     baseURL,
		signer:      signer,
		http:        &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second},
		timeInForce: timeInForce,
		timeout:     time.Duration(timeoutSecs) * time.Second,
	}
}

type createOrderRequest struct {
	Ticker                  string `json:"ticker"`
	ClientOrderID           string `json:"client_order_id,omitempty"`
	Side                    string `json:"side"`
	Count                   string `json:"count"`
	Price                   string `json:"price"`
	TimeInForce             string `json:"time_in_force"`
	SelfTradePreventionType string `json:"self_trade_prevention_type"`
	PostOnly                bool   `json:"post_only"`
	ReduceOnly              bool   `json:"reduce_only"`
	ExchangeIndex           int    `json:"exchange_index"`
}

type createOrderResponse struct {
	OrderID        string `json:"order_id"`
	FillCount      string `json:"fill_count"`
	RemainingCount string `json:"remaining_count"`
}

// CreateOrder submits an IOC order to Kalshi.
func (e *Exchange) CreateOrder(ctx context.Context, req ports.CreateOrderRequest) (*ports.CreateOrderResponse, error) {
	side := "bid"
	if req.Action == "buy_no" {
		side = "ask"
	}

	body := createOrderRequest{
		Ticker:                  req.MarketTicker,
		ClientOrderID:           req.ClientOrderID,
		Side:                    side,
		Count:                   strconv.Itoa(req.Contracts),
		Price:                   fmt.Sprintf("%.4f", float64(req.PriceCents)/100.0),
		TimeInForce:             e.timeInForce,
		SelfTradePreventionType: "taker_at_cross",
		ExchangeIndex:           -1,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal order request: %w", err)
	}

	orderCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	fullURL := e.baseURL + "/portfolio/events/orders"
	httpReq, err := http.NewRequestWithContext(orderCtx, "POST", fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if e.signer != nil {
		headers, err := e.signer.AuthHeaders("POST", "/trade-api/v2/portfolio/events/orders")
		if err != nil {
			return nil, fmt.Errorf("sign request: %w", err)
		}
		for k, v := range headers {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := e.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("kalshi API %d: %s", resp.StatusCode, string(respBody))
	}

	var kalshiResp createOrderResponse
	if err := json.Unmarshal(respBody, &kalshiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	fillCount, _ := strconv.Atoi(kalshiResp.FillCount)
	remainingCount, _ := strconv.Atoi(kalshiResp.RemainingCount)

	status := ports.OrderStatusResting
	if remainingCount == 0 && fillCount > 0 {
		status = ports.OrderStatusFilled
	} else if fillCount > 0 {
		status = ports.OrderStatusPartial
	} else if remainingCount == 0 {
		status = ports.OrderStatusCanceled
	}

	// Fill price equals order price for IOC at the bid/ask.
	// Kalshi doesn't return fill price in the create response; it's in the
	// user orders WS channel. Use the order price as approximation.
	fillPrice := 0
	if fillCount > 0 {
		fillPrice = req.PriceCents
	}

	return &ports.CreateOrderResponse{
		OrderID:        kalshiResp.OrderID,
		Status:         status,
		FillCount:      fillCount,
		FillPriceCents: fillPrice,
	}, nil
}
