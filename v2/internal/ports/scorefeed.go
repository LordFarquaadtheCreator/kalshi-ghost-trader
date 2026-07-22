package ports

import (
	"context"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// ScoreFeed is the interface for score data sources (API-Tennis, Kalshi live-data).
// Implementations emit PointScored events into the match loop via the Merger.
type ScoreFeed interface {
	// Run starts the score feed. Calls onPoint for each point scored.
	Run(ctx context.Context, onPoint func(match.PointScored)) error
	// StartPolling begins tracking a specific event.
	StartPolling(eventTicker string) error
	// StopPolling stops tracking an event.
	StopPolling(eventTicker string) error
}

// MarketStream is the interface for market data streams (Kalshi WS).
type MarketStream interface {
	Run(ctx context.Context) error
	Subscribe(marketTickers []string) error
	Unsubscribe(marketTickers []string) error
}
