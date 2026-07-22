package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
	"gorm.io/gorm"
)

// FeatureRepo persists intent feature logs to the intent_features table.
type FeatureRepo struct {
	db *gorm.DB
}

// NewFeatureRepo creates a feature repository.
func NewFeatureRepo(db *gorm.DB) *FeatureRepo {
	return &FeatureRepo{db: db}
}

// intentFeatureRow maps to the intent_features table.
type intentFeatureRow struct {
	OrderID     int64           `gorm:"primaryKey;column:order_id"`
	FeatureHash string          `gorm:"column:feature_hash"`
	Features    json.RawMessage `gorm:"column:features"`
	ModelID     *int64          `gorm:"column:model_id"`
	Propensity  *float64        `gorm:"column:propensity"`
}

func (intentFeatureRow) TableName() string { return "intent_features" }

// LogFeatures writes a feature log row for an order.
func (r *FeatureRepo) LogFeatures(ctx context.Context, orderID int64, fl ports.FeatureLog) error {
	featuresJSON, err := json.Marshal(fl.Features)
	if err != nil {
		return fmt.Errorf("marshal features: %w", err)
	}

	row := intentFeatureRow{
		OrderID:     orderID,
		FeatureHash: fl.FeatureHash,
		Features:    featuresJSON,
		ModelID:     fl.ModelID,
		Propensity:  fl.Propensity,
	}

	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("log features: %w", err)
	}
	return nil
}

// GetFeatures retrieves the feature log for an order.
func (r *FeatureRepo) GetFeatures(ctx context.Context, orderID int64) (*ports.FeatureLog, error) {
	var row intentFeatureRow
	if err := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&row).Error; err != nil {
		return nil, fmt.Errorf("get features: %w", err)
	}

	var features map[string]float64
	if err := json.Unmarshal(row.Features, &features); err != nil {
		return nil, fmt.Errorf("unmarshal features: %w", err)
	}

	return &ports.FeatureLog{
		FeatureHash: row.FeatureHash,
		Features:    features,
		ModelID:     row.ModelID,
		Propensity:  row.Propensity,
	}, nil
}
