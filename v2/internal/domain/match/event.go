package match

// Event is the sealed interface for all events entering a match loop.
// Implementations are value types — no pointers, no shared mutable state.
type Event interface {
	eventTag()
}

// PriceUpdate is a market price tick from the WS ticker feed.
type PriceUpdate struct {
	MarketTicker string
	PriceCents   int
	TS           int64 // unix ms
}

func (PriceUpdate) eventTag() {}

// Point is a point-by-point score entry. Ported from v1 store.Point as a
// value type (no GORM tags — persistence is the adapter's job).
type Point struct {
	EventTicker  string
	TS           int64
	RecvTS       int64
	SetNumber    int
	GameNumber   int
	PointNumber  int
	Server       int    // 1 = home, 2 = away
	Scorer       int    // 1 = home won, 2 = away won
	HomePoints   string // "0","15","30","40","A"
	AwayPoints   string
	HomeGames    int
	AwayGames    int
	HomeSetGames int
	AwaySetGames int
	IsTiebreak   bool
	IsBreakPoint bool
	IsSetPoint   bool
	IsMatchPoint bool
}

// PointScored is a point-by-point score update from API-Tennis or Kalshi
// live-data.
type PointScored struct {
	EventTicker string
	Point       Point
	TS          int64 // unix ms
}

func (PointScored) eventTag() {}

// LifecycleChange is a market lifecycle transition from the WS feed.
type LifecycleChange struct {
	MarketTicker string
	Type         string // "activated","deactivated","determined","settled","close_date_updated"
	TS           int64  // unix ms
}

func (LifecycleChange) eventTag() {}

// ClockTick is a periodic timer event driving time-based strategies.
type ClockTick struct {
	TS int64 // unix ms
}

func (ClockTick) eventTag() {}
