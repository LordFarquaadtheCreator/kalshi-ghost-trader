package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/orders"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
	"gorm.io/gorm"
)

// OrderRepo persists orders to the orders_v2 table.
type OrderRepo struct {
	db *gorm.DB
}

// NewOrderRepo creates an order repository.
func NewOrderRepo(db *gorm.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

type orderRow struct {
	ID             int64   `gorm:"primaryKey;column:id"`
	ClientOrderID  string  `gorm:"column:client_order_id"`
	TSIntent       int64   `gorm:"column:ts_intent"`
	TSSubmitted    *int64  `gorm:"column:ts_submitted"`
	TSAcked        *int64  `gorm:"column:ts_acked"`
	EventTicker    string  `gorm:"column:event_ticker"`
	MarketTicker   string  `gorm:"column:market_ticker"`
	Strategy       string  `gorm:"column:strategy"`
	Action         string  `gorm:"column:action"`
	Contracts      int     `gorm:"column:contracts"`
	PriceCents     int     `gorm:"column:price_cents"`
	ConvProbBps    int     `gorm:"column:conv_prob_bps"`
	Reason         string  `gorm:"column:reason"`
	Status         string  `gorm:"column:status"`
	GateReason     string  `gorm:"column:gate_reason"`
	IsPaper        bool    `gorm:"column:is_paper"`
	KalshiOrderID  string  `gorm:"column:kalshi_order_id"`
	FillCount      int     `gorm:"column:fill_count"`
	FillPriceCents int     `gorm:"column:fill_price_cents"`
	CreatedTS      int64   `gorm:"column:created_ts"`
	UpdatedTS      int64   `gorm:"column:updated_ts"`
}

func (orderRow) TableName() string { return "orders_v2" }

// Insert persists a new order and returns its ID.
func (r *OrderRepo) Insert(ctx context.Context, o ports.OrderRecord) (int64, error) {
	now := time.Now().UnixMilli()
	row := orderRow{
		ClientOrderID:  o.ClientOrderID,
		TSIntent:       o.TSIntent,
		TSSubmitted:    o.TSSubmitted,
		TSAcked:        o.TSAcked,
		EventTicker:    o.EventTicker,
		MarketTicker:   o.MarketTicker,
		Strategy:       o.Strategy,
		Action:         o.Action,
		Contracts:      o.Contracts,
		PriceCents:     o.PriceCents,
		ConvProbBps:    o.ConvProbBps,
		Reason:         o.Reason,
		Status:         o.Status,
		GateReason:     o.GateReason,
		IsPaper:        o.IsPaper,
		KalshiOrderID:  o.KalshiOrderID,
		FillCount:      o.FillCount,
		FillPriceCents: o.FillPriceCents,
		CreatedTS:      now,
		UpdatedTS:      now,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, fmt.Errorf("insert order: %w", err)
	}
	return row.ID, nil
}

// UpdateStatus transitions an order to a new status, enforcing the legal
// transition map.
func (r *OrderRepo) UpdateStatus(ctx context.Context, id int64, status string, opts ports.UpdateOpts) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row orderRow
		if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
			return fmt.Errorf("get order %d: %w", id, err)
		}
		if !orders.IsLegalTransition(row.Status, status) {
			return fmt.Errorf("transition %s→%s: %w", row.Status, status, orders.ErrIllegalTransition)
		}

		updates := map[string]any{
			"status":     status,
			"updated_ts": time.Now().UnixMilli(),
		}
		if opts.GateReason != "" {
			updates["gate_reason"] = opts.GateReason
		}
		if opts.KalshiOrderID != "" {
			updates["kalshi_order_id"] = opts.KalshiOrderID
		}
		if opts.FillCount > 0 {
			updates["fill_count"] = opts.FillCount
		}
		if opts.FillPriceCents > 0 {
			updates["fill_price_cents"] = opts.FillPriceCents
		}
		if opts.TSSubmitted != nil {
			updates["ts_submitted"] = *opts.TSSubmitted
		}
		if opts.TSAcked != nil {
			updates["ts_acked"] = *opts.TSAcked
		}

		return tx.Model(&orderRow{}).Where("id = ?", id).Updates(updates).Error
	})
}

// GetByID retrieves an order by ID.
func (r *OrderRepo) GetByID(ctx context.Context, id int64) (*ports.OrderRecord, error) {
	var row orderRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, fmt.Errorf("get order %d: %w", id, err)
	}
	return rowToOrderRecord(&row), nil
}

// GetByClientOrderID retrieves an order by client_order_id.
func (r *OrderRepo) GetByClientOrderID(ctx context.Context, coid string) (*ports.OrderRecord, error) {
	var row orderRow
	if err := r.db.WithContext(ctx).Where("client_order_id = ?", coid).First(&row).Error; err != nil {
		return nil, fmt.Errorf("get order by coid %s: %w", coid, err)
	}
	return rowToOrderRecord(&row), nil
}

// GetStaleOrders returns orders in the given statuses older than the threshold.
func (r *OrderRepo) GetStaleOrders(ctx context.Context, statuses []string, olderThan time.Duration) ([]ports.OrderRecord, error) {
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	var rows []orderRow
	if err := r.db.WithContext(ctx).
		Where("status IN ?", statuses).
		Where("ts_intent < ?", cutoff).
		Order("ts_intent ASC").
		Limit(100).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get stale orders: %w", err)
	}
	out := make([]ports.OrderRecord, len(rows))
	for i := range rows {
		out[i] = *rowToOrderRecord(&rows[i])
	}
	return out, nil
}

func rowToOrderRecord(row *orderRow) *ports.OrderRecord {
	return &ports.OrderRecord{
		ID:             row.ID,
		ClientOrderID:  row.ClientOrderID,
		TSIntent:       row.TSIntent,
		TSSubmitted:    row.TSSubmitted,
		TSAcked:        row.TSAcked,
		EventTicker:    row.EventTicker,
		MarketTicker:   row.MarketTicker,
		Strategy:       row.Strategy,
		Action:         row.Action,
		Contracts:      row.Contracts,
		PriceCents:     row.PriceCents,
		ConvProbBps:    row.ConvProbBps,
		Reason:         row.Reason,
		Status:         row.Status,
		GateReason:     row.GateReason,
		IsPaper:        row.IsPaper,
		KalshiOrderID:  row.KalshiOrderID,
		FillCount:      row.FillCount,
		FillPriceCents: row.FillPriceCents,
	}
}
