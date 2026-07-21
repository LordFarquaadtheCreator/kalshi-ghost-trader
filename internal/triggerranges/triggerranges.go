package triggerranges

import (
	"context"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/gorm"
)

// Get returns all trigger ranges for a strategy.
func Get(ctx context.Context, db *gorm.DB, strategy string) ([]store.TriggerRange, error) {
	var ranges []store.TriggerRange
	err := db.WithContext(ctx).Where("strategy = ?", strategy).Order("created_ts").Find(&ranges).Error
	return ranges, err
}

// Replace deletes all existing ranges for a strategy and inserts new ones.
func Replace(ctx context.Context, db *gorm.DB, strategy string, ranges []store.TriggerRange) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("strategy = ?", strategy).Delete(&store.TriggerRange{}).Error; err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		for i := range ranges {
			ranges[i].Strategy = strategy
			ranges[i].CreatedTS = now
		}
		if len(ranges) > 0 {
			return tx.CreateInBatches(&ranges, len(ranges)).Error
		}
		return nil
	})
}

// IsPriceIn checks if a price falls within any enabled trigger range for a strategy.
func IsPriceIn(ctx context.Context, db *gorm.DB, strategy string, price float64) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&store.TriggerRange{}).
		Where("strategy = ? AND enabled = ? AND ? >= min_price AND ? <= max_price",
			strategy, true, price, price).Count(&count).Error
	return count > 0, err
}

// Has returns true if a strategy has any trigger ranges configured (enabled or not).
func Has(ctx context.Context, db *gorm.DB, strategy string) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&store.TriggerRange{}).
		Where("strategy = ?", strategy).Count(&count).Error
	return count > 0, err
}
