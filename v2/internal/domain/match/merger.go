package match

import "sync/atomic"

// pointKey is the dedup key for PointScored events from multiple sources.
type pointKey struct {
	EventTicker string
	SetsHome    int
	SetsAway    int
	HomeGames   int
	AwayGames   int
	HomePoints  string
	AwayPoints  string
}

// Merger dedupes PointScored events from two score sources (primary
// API-Tennis, backup Kalshi live-data). First arrival forwards to the loop;
// duplicates are dropped with a counter.
//
// Not safe for concurrent use — owned by the loop goroutine, which is the
// only caller of Forward.
type Merger struct {
	seen     map[pointKey]bool
	DupCount atomic.Int64
}

// NewMerger creates a score-source merger.
func NewMerger() *Merger {
	return &Merger{seen: make(map[pointKey]bool, 256)}
}

// Forward returns the event if it's the first occurrence for its score state,
// or nil if it's a duplicate.
func (m *Merger) Forward(ev PointScored) *PointScored {
	key := pointKey{
		EventTicker: ev.EventTicker,
		SetsHome:    ev.Point.HomeSetGames,
		SetsAway:    ev.Point.AwaySetGames,
		HomeGames:   ev.Point.HomeGames,
		AwayGames:   ev.Point.AwayGames,
		HomePoints:  ev.Point.HomePoints,
		AwayPoints:  ev.Point.AwayPoints,
	}
	if m.seen[key] {
		m.DupCount.Add(1)
		return nil
	}
	m.seen[key] = true
	return &ev
}
