// Package strategy defines the strategy interface and shared match state
// for the single-threaded event model.
//
// Strategies are plain structs with no mutexes — the event loop guarantees
// single-threaded access. OnEvent receives an event and the shared read-only
// match view; it returns intents (not side-effectful order emissions).
package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// Strategy processes events for a single match. OnEvent is called from the
// loop goroutine only — no concurrent access, no mutexes needed.
//
// The strategy may mutate only its own opaque state entry in State.StrategyState.
// The shared match view (prices, score, occurrence) is read-only.
type Strategy interface {
	Name() string
	OnEvent(ev match.Event, s *State) []match.Intent
}

// State is the per-match shared state passed to strategies on each event.
// The match view fields are read-only; StrategyState is a per-strategy
// opaque map where each strategy stores its own mutable state.
type State struct {
	// MatchView is the shared read-only view of the match.
	MatchView MatchView

	// StrategyState holds per-strategy opaque state. Keyed by strategy name.
	// A strategy may mutate only its own entry.
	StrategyState map[string]any
}

// MatchView is the read-only shared view of the match state. Updated by
// the event loop before dispatching to strategies.
type MatchView struct {
	EventTicker   string
	MarketTickers []string // [home_ticker, away_ticker]
	OccurrenceTS  int64    // unix ms

	// Prices: market_ticker -> latest price in cents (1..99)
	Prices map[string]int

	// Price timestamps: market_ticker -> last update TS (unix ms)
	PriceTS map[string]int64

	// Current score state (updated by PointScored events)
	SetsHome      int
	SetsAway      int
	GamesHome     int
	GamesAway     int
	HomePoints    string
	AwayPoints    string
	Server        int // 1=home, 2=away
	IsTiebreak    bool
	SetNumber     int
	GameNumber    int
	PointNumber   int
	Scorer        int
	IsBreakPoint  bool
	IsSetPoint    bool
	IsMatchPoint  bool
}

// Get returns the strategy's opaque state, or nil if not set.
func (s *State) Get(name string) any {
	return s.StrategyState[name]
}

// Set stores the strategy's opaque state.
func (s *State) Set(name string, v any) {
	s.StrategyState[name] = v
}
