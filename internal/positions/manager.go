// Package positions implements position lifecycle for the sell-to-close
// pipeline. One Position per (market_ticker, strategy, is_real).
//
// Buys (side="open") add to FilledBuyCount and reweight AvgEntryPrice.
// Sells (side="close") add to FilledSellCount, compute realized PnL
// = (avg_exit - avg_entry) * fill_count * 100, reweight AvgExitPrice.
// When FilledSellCount == FilledBuyCount, status -> "closed".
//
// Sell-to-close only: rejects sells with no open long position (no naked
// shorts).
//
// At market settlement, reconciler calls Settle to mark any remaining
// open contracts (FilledBuyCount - FilledSellCount) at $1 if won or $0
// if lost, computing settlement PnL.
//
// Backward compat: legacy orders (side=NULL, no position_id) bypass this
// package. Reconciler's legacy ResolveRealOrders/ResolveSimulatedOrders
// still handles them.
package positions

import (
	"context"
	"errors"
	"fmt"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"gorm.io/gorm"
)

// ErrNoOpenPosition is returned when a sell arrives with no open long.
// Sell-to-close only — no naked shorts.
var ErrNoOpenPosition = errors.New("positions: no open position to close")

// ErrInsufficientSize is returned when a sell exceeds the open contract count.
var ErrInsufficientSize = errors.New("positions: sell count exceeds open contracts")

// Manager wraps a *gorm.DB for position lifecycle operations.
// All methods are transactional — they read the position, mutate it, and
// write it back atomically.
type Manager struct {
	db  *gorm.DB
	now func() int64
}

// New creates a Manager bound to the given GORM handle.
func New(db *gorm.DB) *Manager {
	return &Manager{db: db, now: nowMillis}
}

// nowMillis is overridable for tests.
var nowMillis = func() int64 {
	// store.nowMillis is not exported; use time.Now directly.
	return timeNowMillis()
}

// ApplyBuy records a buy fill against a position. Creates the position if
// it doesn't exist. Updates FilledBuyCount and reweights AvgEntryPrice.
// Returns the position ID. Idempotent on position row via unique constraint.
//
// fillCount: number of contracts filled (use SuggestedSize for paper, FillCount for real).
// price: average fill price per contract (0..1).
func (m *Manager) ApplyBuy(
	ctx context.Context,
	matchTicker, marketTicker, strategy string,
	isReal bool,
	fillCount, price float64,
) (positionID int64, err error) {
	if fillCount <= 0 {
		return 0, fmt.Errorf("positions: buy fill_count must be positive, got %f", fillCount)
	}
	if price < 0 || price > 1 {
		return 0, fmt.Errorf("positions: buy price out of range [0,1]: %f", price)
	}

	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p store.Position
		lookup := tx.Where(
			"match_ticker = ? AND market_ticker = ? AND strategy = ? AND is_real = ?",
			matchTicker, marketTicker, strategy, isReal,
		).First(&p)

		if lookup.Error != nil {
			if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
				return lookup.Error
			}
			// Create new position.
			p = store.Position{
				MatchTicker:    matchTicker,
				MarketTicker:   marketTicker,
				Strategy:       strategy,
				IsReal:         isReal,
				FilledBuyCount: fillCount,
				AvgEntryPrice:  price,
				Status:         store.PositionStatusOpen,
				OpenedTS:       m.now(),
			}
			if err := tx.Create(&p).Error; err != nil {
				return fmt.Errorf("positions: create position: %w", err)
			}
			positionID = p.ID
			return nil
		}

		if p.Status == store.PositionStatusSettled {
			return fmt.Errorf("positions: cannot buy into settled position %d", p.ID)
		}

		// Reweight avg entry: new_avg = (old_count*old_avg + fill*price) / new_count
		newCount := p.FilledBuyCount + fillCount
		if newCount > 0 {
			p.AvgEntryPrice = (p.FilledBuyCount*p.AvgEntryPrice + fillCount*price) / newCount
		}
		p.FilledBuyCount = newCount
		// Reopen if was closed (e.g. sold out, then bought again).
		if p.Status == store.PositionStatusClosed {
			p.Status = store.PositionStatusOpen
			p.ClosedTS = 0
		}
		if err := tx.Save(&p).Error; err != nil {
			return fmt.Errorf("positions: update position on buy: %w", err)
		}
		positionID = p.ID
		return nil
	})
	return positionID, err
}

// ApplySell records a sell fill against an open long position. Computes
// realized PnL = (sell_price - avg_entry) * fill_count * 100. Updates
// FilledSellCount and AvgExitPrice. When FilledSellCount == FilledBuyCount,
// status -> "closed".
//
// Returns (positionID, realizedPNLCents, remainingOpenCount).
func (m *Manager) ApplySell(
	ctx context.Context,
	matchTicker, marketTicker, strategy string,
	isReal bool,
	fillCount, price float64,
) (positionID int64, realizedPNLCents int64, remainingOpen float64, err error) {
	if fillCount <= 0 {
		return 0, 0, 0, fmt.Errorf("positions: sell fill_count must be positive, got %f", fillCount)
	}
	if price < 0 || price > 1 {
		return 0, 0, 0, fmt.Errorf("positions: sell price out of range [0,1]: %f", price)
	}

	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p store.Position
		if err := tx.Where(
			"match_ticker = ? AND market_ticker = ? AND strategy = ? AND is_real = ?",
			matchTicker, marketTicker, strategy, isReal,
		).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoOpenPosition
			}
			return err
		}

		if p.Status == store.PositionStatusSettled {
			return fmt.Errorf("positions: cannot sell settled position %d", p.ID)
		}

		openContracts := p.FilledBuyCount - p.FilledSellCount
		if openContracts <= 0 {
			return ErrNoOpenPosition
		}
		if fillCount > openContracts {
			return fmt.Errorf("%w: sell=%f open=%f", ErrInsufficientSize, fillCount, openContracts)
		}

		// Realized PnL on this sell: (sell_price - avg_entry) * fill_count * 100 cents
		realizedPNLCents = int64((price-p.AvgEntryPrice)*fillCount*100 + 0.5)

		// Reweight avg exit
		newSellCount := p.FilledSellCount + fillCount
		if newSellCount > 0 {
			p.AvgExitPrice = (p.FilledSellCount*p.AvgExitPrice + fillCount*price) / newSellCount
		}
		p.FilledSellCount = newSellCount
		p.RealizedPNLCents += realizedPNLCents

		remainingOpen = p.FilledBuyCount - p.FilledSellCount
		if remainingOpen == 0 {
			p.Status = store.PositionStatusClosed
			p.ClosedTS = m.now()
		}
		if err := tx.Save(&p).Error; err != nil {
			return fmt.Errorf("positions: update position on sell: %w", err)
		}
		positionID = p.ID
		return nil
	})
	return positionID, realizedPNLCents, remainingOpen, err
}

// Settle is called by the reconciler at market settlement. Closes any
// remaining open contracts (FilledBuyCount - FilledSellCount) at $1 if
// won, $0 if lost. Adds settlement PnL to RealizedPNLCents and marks
// status -> "settled". No-op if position already settled.
//
// Returns (positionID, settlementPNLCents, remainingContracts).
func (m *Manager) Settle(
	ctx context.Context,
	matchTicker, marketTicker, strategy string,
	isReal bool,
	won bool,
) (positionID int64, settlementPNLCents int64, remainingContracts float64, err error) {
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p store.Position
		if err := tx.Where(
			"match_ticker = ? AND market_ticker = ? AND strategy = ? AND is_real = ?",
			matchTicker, marketTicker, strategy, isReal,
		).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // no position for this market/strategy — legacy order path
			}
			return err
		}

		if p.Status == store.PositionStatusSettled {
			return nil // idempotent
		}

		remainingContracts = p.FilledBuyCount - p.FilledSellCount
		if remainingContracts < 0 {
			remainingContracts = 0
		}

		// Settlement PnL on remaining open contracts:
		// won: payout $1/contract, cost = avg_entry * count => pnl = (1 - avg_entry) * count * 100
		// lost: payout $0 => pnl = -avg_entry * count * 100
		if remainingContracts > 0 {
			if won {
				settlementPNLCents = int64((1.0-p.AvgEntryPrice)*remainingContracts*100 + 0.5)
			} else {
				settlementPNLCents = -int64(p.AvgEntryPrice*remainingContracts*100 + 0.5)
			}
			p.RealizedPNLCents += settlementPNLCents
		}

		p.Status = store.PositionStatusSettled
		p.ClosedTS = m.now()
		if err := tx.Save(&p).Error; err != nil {
			return fmt.Errorf("positions: settle position: %w", err)
		}
		positionID = p.ID
		return nil
	})
	return positionID, settlementPNLCents, remainingContracts, err
}

// GetOpen returns all open positions for a market (both real + paper).
// Used by strategies to check if they have an exit opportunity.
func (m *Manager) GetOpen(ctx context.Context, marketTicker string) ([]store.Position, error) {
	var out []store.Position
	err := m.db.WithContext(ctx).
		Where("market_ticker = ? AND status = ?", marketTicker, store.PositionStatusOpen).
		Find(&out).Error
	return out, err
}

// GetOpenForStrategy returns the open position for a specific strategy+market+isReal, or nil.
func (m *Manager) GetOpenForStrategy(
	ctx context.Context, marketTicker, strategy string, isReal bool,
) (*store.Position, error) {
	var p store.Position
	err := m.db.WithContext(ctx).
		Where("market_ticker = ? AND strategy = ? AND is_real = ? AND status = ?",
			marketTicker, strategy, isReal, store.PositionStatusOpen).
		First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}
