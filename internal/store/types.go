package store

// Event maps a tennis match event row.
type Event struct {
	EventTicker       string
	SeriesTicker      string
	Title             string
	SubTitle          string
	Competition       string
	CompetitionScope  string
	MutuallyExclusive bool
}

// Market maps a tennis match market row. Two per event (one per player).
type Market struct {
	MarketTicker     string
	EventTicker      string
	SeriesTicker     string
	PlayerName       string
	TennisCompetitor string
	Status           string
	OccurrenceTS     int64
	OpenTS           int64
	CloseTS          int64
	Result           string
	SettlementTS     int64
	SettlementValue  string
}

// Tick maps a single WS message (ticker, trade, orderbook) stored verbatim.
type Tick struct {
	TS                 int64
	RecvTS             int64
	MarketTicker       string
	MsgType            string
	SID                int64
	Seq                int64
	Price              float64
	YesBid             float64
	YesAsk             float64
	YesBidSize         float64
	YesAskSize         float64
	Volume             float64
	OpenInterest       float64
	DollarVolume       int64
	DollarOpenInterest int64
	LastTradeSize      float64
	TradeID            string
	NoPrice            float64
	TakerSide          string
	TakerOutcomeSide   string
	TakerBookSide      string
	Payload            string
}

// LifecycleEvent maps a market_lifecycle_v2 WS event.
type LifecycleEvent struct {
	TS              int64
	RecvTS          int64
	MarketTicker    string
	EventType       string
	Result          string
	OpenTS          int64
	CloseTS         int64
	DeterminationTS int64
	SettledTS       int64
	SettlementValue string
	Payload         string
}

// EventLifecycleEvent maps an event_lifecycle WS message (event creation).
type EventLifecycleEvent struct {
	TS           int64
	RecvTS       int64
	EventTicker  string
	SeriesTicker string
	Title        string
	Subtitle     string
	Payload      string
}

// OrderbookEvent maps an orderbook_snapshot or orderbook_delta WS message.
// Snapshot: price/delta/side are zero — full levels in payload.
// Delta: price/delta/side extracted as hot fields.
type OrderbookEvent struct {
	TS           int64
	RecvTS       int64
	MarketTicker string
	MsgType      string // "orderbook_snapshot" or "orderbook_delta"
	SID          int64
	Seq          int64
	Price        float64
	Delta        float64
	Side         string
	Payload      string
}

// FSMatch maps a flashscore_matches row. Links Kalshi event_ticker to
// FlashScore's internal match ID.
type FSMatch struct {
	FSMatchID     string
	EventTicker   string // nullable until mapped to Kalshi event
	HomePlayer    string
	AwayPlayer    string
	Tournament    string
	Surface       string
	Category      string
	StartTS       int64
	FSStatus      int
	LastPolledTS  int64
}

// Point maps a single tennis point from FlashScore point-by-point data.
// Server/scorer use 1=home, 2=away. Ticker refers to Kalshi event_ticker.
type Point struct {
	MatchTicker   string
	FSMatchID     string
	TsMs          int64 // 0 = unknown (historical backfill)
	RecvTS        int64
	SetNumber     int
	GameNumber    int
	PointNumber   int
	Server        int // 1 or 2
	Scorer        int // 1 or 2
	HomePoints    string
	AwayPoints    string
	HomeGames     int
	AwayGames     int
	HomeSetGames  int // nullable
	AwaySetGames  int // nullable
	IsTiebreak    bool
	IsBreakPoint  bool
	Payload       string
}
