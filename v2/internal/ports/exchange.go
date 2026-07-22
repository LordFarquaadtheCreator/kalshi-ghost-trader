package ports

import (
	"context"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// Exchange is the trading interface the order worker depends on.
// Implementations live in adapters/kalshi.
type Exchange interface {
	// CreateOrder submits an IOC order. Returns fill count, fill price cents,
	// and the exchange's order ID. A canceled order returns fillCount=0.
	CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error)
}

// CreateOrderRequest is the input to CreateOrder.
type CreateOrderRequest struct {
	ClientOrderID string
	MarketTicker  string
	Action        string // "buy" or "buy_no"
	Contracts     int
	PriceCents    int
}

// CreateOrderResponse is the output of CreateOrder.
type CreateOrderResponse struct {
	OrderID       string
	Status        OrderStatus
	FillCount     int
	FillPriceCents int
}

// OrderStatus is the status returned by the exchange.
type OrderStatus string

const (
	OrderStatusFilled   OrderStatus = "filled"
	OrderStatusPartial  OrderStatus = "partial"
	OrderStatusCanceled OrderStatus = "canceled"
	OrderStatusResting  OrderStatus = "resting"
)

// OrderRepo is the persistence interface for orders.
type OrderRepo interface {
	// Insert persists a new order in 'intent' status and returns its ID.
	Insert(ctx context.Context, o OrderRecord) (int64, error)
	// UpdateStatus transitions an order to a new status, enforcing the legal
	// transition map. Returns an error if the transition is illegal.
	UpdateStatus(ctx context.Context, id int64, status string, opts UpdateOpts) error
	// GetByID retrieves an order by ID.
	GetByID(ctx context.Context, id int64) (*OrderRecord, error)
	// GetByClientOrderID retrieves an order by client_order_id.
	GetByClientOrderID(ctx context.Context, coid string) (*OrderRecord, error)
	// GetStaleOrders returns orders in the given statuses older than the threshold.
	GetStaleOrders(ctx context.Context, statuses []string, olderThan time.Duration) ([]OrderRecord, error)
}

// OrderRecord is the persisted order state.
type OrderRecord struct {
	ID             int64
	ClientOrderID  string
	TSIntent       int64
	TSSubmitted    *int64
	TSAcked        *int64
	EventTicker    string
	MarketTicker   string
	Strategy       string
	Action         string
	Contracts      int
	PriceCents     int
	ConvProbBps    int
	Reason         string
	Status         string
	GateReason     string
	IsPaper        bool
	KalshiOrderID  string
	FillCount      int
	FillPriceCents int
}

// UpdateOpts holds optional fields for status updates.
type UpdateOpts struct {
	GateReason     string
	KalshiOrderID  string
	FillCount      int
	FillPriceCents int
	TSSubmitted    *int64
	TSAcked        *int64
}

// LedgerRepo is the interface the order worker depends on.
type LedgerRepo interface {
	HoldForOrder(ctx context.Context, orderID int64, spendCents int64) error
	ReleaseHold(ctx context.Context, orderID int64, releaseCents int64) error
	RecordFill(ctx context.Context, orderID int64, fillCostCents int64) error
	RecordSettlement(ctx context.Context, orderID int64, payoutCents int64) error
	GetBalance(ctx context.Context) (int64, error)
	CheckInvariants(ctx context.Context) error
}

// FeatureLog is the feature vector + model identity written alongside every intent.
type FeatureLog struct {
	FeatureHash string
	Features    map[string]float64
	ModelID     *int64
	Propensity  *float64
}

// FeatureRepo persists intent feature logs.
type FeatureRepo interface {
	LogFeatures(ctx context.Context, orderID int64, fl FeatureLog) error
}

// BookSnapshot is the top-of-book state at a point in time.
type BookSnapshot struct {
	BestBidCents int
	BestAskCents int
	BestBidSize  int
	BestAskSize  int
}

// BookLookup provides top-of-book state for a market at the current instant.
// Used by the realistic paper fill model (A.8).
type BookLookup interface {
	Lookup(ctx context.Context, marketTicker string) (*BookSnapshot, error)
}

// IntentFromMatch converts a match.Intent to an OrderRecord seed.
func IntentFromMatch(i match.Intent, eventTicker string, isPaper bool) OrderRecord {
	return OrderRecord{
		EventTicker:  eventTicker,
		MarketTicker: i.MarketTicker,
		Strategy:     i.Strategy,
		Action:       i.Action,
		PriceCents:   i.PriceCents,
		ConvProbBps:  i.ConvProbBps,
		Reason:       i.Reason,
		Status:       "intent",
		IsPaper:      isPaper,
	}
}
