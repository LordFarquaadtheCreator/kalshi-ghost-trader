package flashscore

// FeedMatch is a parsed match from the FlashScore daily feed (f_2_*).
type FeedMatch struct {
	ID         string // AA field — FlashScore internal match ID
	HomeName   string // CX/AE field
	AwayName   string // AF/FK field
	HomeID     string // PX field
	AwayID     string // PY field
	HomeKW     string // WU field — URL keyword
	AwayKW     string // WV field
	Tournament string // ZA field — tournament name
	StartTS    int64  // AD field — unix seconds
	StageType  int    // AB field — 1=finished, 2=in-progress, 3=upcoming
	StageID    int    // AC field
	Category   string // derived from ZA prefix (ATP, WTA, etc.)
	Surface    string // derived from ZA suffix
}

// PointData is a single parsed point from the df_mh_1 endpoint.
type PointData struct {
	SetNumber    int
	GameNumber   int
	PointNumber  int
	Server       int    // 1=home, 2=away
	Scorer       int    // 1=home, 2=away
	HomePoints   string // "0","15","30","40","A"
	AwayPoints   string
	HomeGames    int // games won by home in this set at this point
	AwayGames    int
	IsTiebreak   bool
	IsBreakPoint bool
	RawHL        string
}

// MatchPoints is the full point-by-point data for a match.
type MatchPoints struct {
	FSMatchID string
	Sets      []SetPoints
}

// SetPoints holds all points in one set.
type SetPoints struct {
	SetNumber    int
	HomeGames    int // final games won by home in this set
	AwayGames    int // final games won by away in this set
	Points       []PointData
}
