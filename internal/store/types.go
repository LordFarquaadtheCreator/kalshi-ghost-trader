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

// Order maps a simulated buy order from the match point signal algorithm.
// Traceable to the match via match_ticker (event_ticker) and market_ticker.
type Order struct {
	TS            int64
	MatchTicker   string  // Kalshi event_ticker
	MarketTicker  string  // Kalshi market_ticker (YES side)
	Action        string  // "buy"
	Context       string  // match point context description
	ConvProb      float64 // converted probability (0-1)
	MarketPrice   float64 // market YES price (0-1)
	EdgeCents     int     // edge in cents
	SuggestedSize float64 // suggested buy size (shares)
	SetNumber     int     // set when signal fired
	Strategy      string  // strategy name that generated this order
	Payload       string  // extra debug info (JSON)
}
