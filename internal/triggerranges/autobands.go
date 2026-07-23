package triggerranges

import (
	"context"
	"fmt"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/gorm"
)

// AddIfMissing inserts ranges that don't already exist for the same
// (strategy, min_price, max_price). Existing rows are left untouched —
// their enabled flag and source are preserved. Returns the count inserted.
//
// Used by the pricebands cron to auto-add qualifying bands without
// clobbering manual dashboard edits.
func AddIfMissing(ctx context.Context, db *gorm.DB, ranges []store.TriggerRange) (int, error) {
	if len(ranges) == 0 {
		return 0, nil
	}

	// Collect strategies involved so we only query those.
	strategies := map[string]bool{}
	for _, r := range ranges {
		strategies[r.Strategy] = true
	}

	existing := map[string]bool{}
	for strat := range strategies {
		var rows []store.TriggerRange
		if err := db.WithContext(ctx).Where("strategy = ?", strat).Find(&rows).Error; err != nil {
			return 0, err
		}
		for _, r := range rows {
			existing[bandKey(r.Strategy, r.MinPrice, r.MaxPrice)] = true
		}
	}

	now := time.Now().UnixMilli()
	var toInsert []store.TriggerRange
	for _, r := range ranges {
		if existing[bandKey(r.Strategy, r.MinPrice, r.MaxPrice)] {
			continue
		}
		r.CreatedTS = now
		toInsert = append(toInsert, r)
	}

	if len(toInsert) == 0 {
		return 0, nil
	}
	if err := db.WithContext(ctx).CreateInBatches(&toInsert, len(toInsert)).Error; err != nil {
		return 0, err
	}
	return len(toInsert), nil
}

func bandKey(strategy string, minPrice, maxPrice float64) string {
	return fmt.Sprintf("%s|%.4f|%.4f", strategy, minPrice, maxPrice)
}
