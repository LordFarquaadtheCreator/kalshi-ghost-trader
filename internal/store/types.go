package store

// Event maps a tennis match event row.
type Event struct {
	EventTicker       string `gorm:"primaryKey;column:event_ticker"`
	SeriesTicker      string `gorm:"column:series_ticker"`
	Title             string `gorm:"column:title"`
	SubTitle          string `gorm:"column:sub_title"`
	Competition       string `gorm:"column:competition"`
	CompetitionScope  string `gorm:"column:competition_scope"`
	MutuallyExclusive bool   `gorm:"column:mutually_exclusive"`
	FirstSeenTS       int64  `gorm:"column:first_seen_ts"`
	LastUpdatedTS     int64  `gorm:"column:last_updated_ts"`
	Coverage          string `gorm:"column:coverage"`
}

// Market maps a tennis match market row. Two per event (one per player).
type Market struct {
	MarketTicker     string `gorm:"primaryKey;column:market_ticker"`
	EventTicker      string `gorm:"column:event_ticker"`
	SeriesTicker     string `gorm:"column:series_ticker"`
	PlayerName       string `gorm:"column:player_name"`
	TennisCompetitor string `gorm:"column:tennis_competitor"`
	Status           string `gorm:"column:status"`
	OccurrenceTS     int64  `gorm:"column:occurrence_ts"`
	OpenTS           int64  `gorm:"column:open_ts"`
	CloseTS          int64  `gorm:"column:close_ts"`
	Result           string `gorm:"column:result"`
	SettlementTS     int64  `gorm:"column:settlement_ts"`
	SettlementValue  string `gorm:"column:settlement_value"`
	FirstSeenTS      int64  `gorm:"column:first_seen_ts"`
	LastUpdatedTS    int64  `gorm:"column:last_updated_ts"`
	IsDeactivated    *bool  `gorm:"column:is_deactivated"`
}

// Tick maps a single WS message (ticker, trade, orderbook) stored verbatim.
type Tick struct {
	ID                 int64   `gorm:"primaryKey;autoIncrement;column:id"`
	TS                 int64   `gorm:"column:ts"`
	RecvTS             int64   `gorm:"column:recv_ts"`
	MarketTicker       string  `gorm:"column:market_ticker"`
	MsgType            string  `gorm:"column:msg_type"`
	SID                int64   `gorm:"column:sid"`
	Seq                int64   `gorm:"column:seq"`
	Price              float64 `gorm:"column:price"`
	YesBid             float64 `gorm:"column:yes_bid"`
	YesAsk             float64 `gorm:"column:yes_ask"`
	YesBidSize         float64 `gorm:"column:yes_bid_size"`
	YesAskSize         float64 `gorm:"column:yes_ask_size"`
	Volume             float64 `gorm:"column:volume"`
	OpenInterest       float64 `gorm:"column:open_interest"`
	DollarVolume       int64   `gorm:"column:dollar_volume"`
	DollarOpenInterest int64   `gorm:"column:dollar_open_interest"`
	LastTradeSize      float64 `gorm:"column:last_trade_size"`
	TradeID            string  `gorm:"column:trade_id"`
	NoPrice            float64 `gorm:"column:no_price"`
	TakerSide          string  `gorm:"column:taker_side"`
	TakerOutcomeSide   string  `gorm:"column:taker_outcome_side"`
	TakerBookSide      string  `gorm:"column:taker_book_side"`
	Payload            string  `gorm:"column:payload"`
}

// LifecycleEvent maps a market_lifecycle_v2 WS event.
type LifecycleEvent struct {
	ID              int64  `gorm:"primaryKey;autoIncrement;column:id"`
	TS              int64  `gorm:"column:ts"`
	RecvTS          int64  `gorm:"column:recv_ts"`
	MarketTicker    string `gorm:"column:market_ticker"`
	EventType       string `gorm:"column:event_type"`
	Result          string `gorm:"column:result"`
	OpenTS          int64  `gorm:"column:open_ts"`
	CloseTS         int64  `gorm:"column:close_ts"`
	DeterminationTS int64  `gorm:"column:determination_ts"`
	SettledTS       int64  `gorm:"column:settled_ts"`
	SettlementValue string `gorm:"column:settlement_value"`
	IsDeactivated   *bool  `gorm:"column:is_deactivated"`
	Payload         string `gorm:"column:payload"`
}

// EventLifecycleEvent maps an event_lifecycle WS message (event creation).
type EventLifecycleEvent struct {
	ID           int64  `gorm:"primaryKey;autoIncrement;column:id"`
	TS           int64  `gorm:"column:ts"`
	RecvTS       int64  `gorm:"column:recv_ts"`
	EventTicker  string `gorm:"column:event_ticker"`
	SeriesTicker string `gorm:"column:series_ticker"`
	Title        string `gorm:"column:title"`
	Subtitle     string `gorm:"column:subtitle"`
	Payload      string `gorm:"column:payload"`
}

// OrderbookEvent maps an orderbook_snapshot or orderbook_delta WS message.
// Snapshot: price/delta/side are zero — full levels in payload.
// Delta: price/delta/side extracted as hot fields.
type OrderbookEvent struct {
	ID           int64   `gorm:"primaryKey;autoIncrement;column:id"`
	TS           int64   `gorm:"column:ts"`
	RecvTS       int64   `gorm:"column:recv_ts"`
	MarketTicker string  `gorm:"column:market_ticker"`
	MsgType      string  `gorm:"column:msg_type"` // "orderbook_snapshot" or "orderbook_delta"
	SID          int64   `gorm:"column:sid"`
	Seq          int64   `gorm:"column:seq"`
	Price        float64 `gorm:"column:price"`
	Delta        float64 `gorm:"column:delta"`
	Side         string  `gorm:"column:side"`
	Payload      string  `gorm:"column:payload"`
}

// Point maps a single point-by-point score entry from the points table.
type Point struct {
	ID           int64  `gorm:"primaryKey;autoIncrement;column:id"`
	MatchTicker  string `gorm:"column:match_ticker"` // Kalshi event_ticker
	FSMatchID    string `gorm:"column:fs_match_id"`  // FlashScore/API-Tennis match ID
	TS           int64  `gorm:"column:ts_ms"`        // unix ms (may be 0 if historical)
	RecvTS       int64  `gorm:"column:recv_ts"`      // when we stored it
	SetNumber    int    `gorm:"column:set_number"`   // 1-based
	GameNumber   int    `gorm:"column:game_number"`  // 1-based within set
	PointNumber  int    `gorm:"column:point_number"` // 1-based within game
	Server       int    `gorm:"column:server"`       // 1 = home, 2 = away
	Scorer       int    `gorm:"column:scorer"`       // 1 = home won point, 2 = away won point
	HomePoints   string `gorm:"column:home_points"`  // "0", "15", "30", "40", "A"
	AwayPoints   string `gorm:"column:away_points"`
	HomeGames    int    `gorm:"column:home_games"`     // games won by home in this set at this point
	AwayGames    int    `gorm:"column:away_games"`     // games won by away in this set at this point
	HomeSetGames int    `gorm:"column:home_set_games"` // final games in completed sets before this one
	AwaySetGames int    `gorm:"column:away_set_games"`
	IsTiebreak   bool   `gorm:"column:is_tiebreak"`
	IsBreakPoint bool   `gorm:"column:is_break_point"`
	IsSetPoint   bool   `gorm:"column:is_set_point"`
	IsMatchPoint bool   `gorm:"column:is_match_point"`
	Payload      string `gorm:"column:payload"`
}

// OrderSide values: "open" = buy to open/add to a long position,
// "close" = sell to close an existing long. NULL on legacy orders is
// treated as "open" by the position pipeline.
const (
	OrderSideOpen  = "open"
	OrderSideClose = "close"
)

// Order maps a simulated buy order from the match point signal algorithm.
// Traceable to the match via match_ticker (event_ticker) and market_ticker.
type Order struct {
	ID                     int64   `gorm:"primaryKey;autoIncrement;column:id"`
	TS                     int64   `gorm:"column:ts"`
	MatchTicker            string  `gorm:"column:match_ticker"`
	MarketTicker           string  `gorm:"column:market_ticker"`
	MatchTitle             string  `gorm:"column:match_title"`
	PlayerName             string  `gorm:"column:player_name"`
	Action                 string  `gorm:"column:action"`        // "buy" or "sell"
	Side                   string  `gorm:"column:side"`          // "open" or "close"; NULL = legacy open
	Context                string  `gorm:"column:context"`
	ConvProb               float64 `gorm:"column:conv_prob"`
	MarketPrice            float64 `gorm:"column:market_price"`
	FillPrice              float64 `gorm:"column:fill_price"` // actual Kalshi fill price per contract; 0 = not yet fetched
	EdgeCents              int     `gorm:"column:edge_cents"`
	SuggestedSize          float64 `gorm:"column:suggested_size"`
	SetNumber              int     `gorm:"column:set_number"`
	Strategy               string  `gorm:"column:strategy"`
	Payload                string  `gorm:"column:payload"`
	Bankroll               float64 `gorm:"column:bankroll"`
	KellyFraction          float64 `gorm:"column:kelly_fraction"`
	IsReal                 bool    `gorm:"column:is_real"`
	KalshiOrderID          string  `gorm:"column:kalshi_order_id"`
	FillCount              float64 `gorm:"column:fill_count"`
	OrderStatus            string  `gorm:"column:order_status"`
	ResolvedPNLCents       int64   `gorm:"column:resolved_pnl_cents"`
	PoolBalanceBeforeCents int64   `gorm:"column:pool_balance_before_cents"`
	PoolBalanceAfterCents  int64   `gorm:"column:pool_balance_after_cents"`
	UnfilledRefundedCents  int64   `gorm:"column:unfilled_refunded_cents"`
	PositionID             *int64  `gorm:"column:position_id"`
	PairID                 string  `gorm:"column:pair_id"` // links arb legs (cross-arb); empty for single-leg orders
	Result                 string  `gorm:"column:result"`
	SettledTS              int64   `gorm:"column:settled_ts"`
	PnLUpdatedTS           int64   `gorm:"column:pnl_updated_ts"`

	// EmitTS stamps when the order was emitted by a strategy (unix ms).
	// Not persisted — used for hop2 latency measurement (strategy-done → order-persisted).
	EmitTS int64 `gorm:"-"`
}

// Position aggregates buys + sells for one (market, strategy, is_real).
// One row per position. Buys add to FilledBuyCount and reweight AvgEntryPrice.
// Sells add to FilledSellCount, compute realized PnL, reweight AvgExitPrice.
// When FilledSellCount == FilledBuyCount, status -> "closed".
// At market settlement, reconciler settles any remaining open contracts.
type Position struct {
	ID                 int64   `gorm:"primaryKey;autoIncrement;column:id"`
	MatchTicker        string  `gorm:"column:match_ticker"`
	MarketTicker       string  `gorm:"column:market_ticker"`
	Strategy           string  `gorm:"column:strategy"`
	IsReal             bool    `gorm:"column:is_real"`
	Action             string  `gorm:"column:action"` // "buy" (YES) or "buy_no" (NO); empty = legacy "buy"
	FilledBuyCount     float64 `gorm:"column:filled_buy_count"`
	FilledSellCount    float64 `gorm:"column:filled_sell_count"`
	AvgEntryPrice      float64 `gorm:"column:avg_entry_price"`
	AvgExitPrice       float64 `gorm:"column:avg_exit_price"`
	RealizedPNLCents   int64   `gorm:"column:realized_pnl_cents"`
	Status             string  `gorm:"column:status"` // open, closed, settled
	OpenedTS           int64   `gorm:"column:opened_ts"`
	ClosedTS           int64   `gorm:"column:closed_ts"`
}

// Position status constants.
const (
	PositionStatusOpen    = "open"
	PositionStatusClosed  = "closed"
	PositionStatusSettled = "settled"
)

// KalshiScore is a live score snapshot from Kalshi's /live_data endpoint.
// Point-level granularity via PointsHome/PointsAway (0/15/30/40/50 where 50=A).
// Backup source when API-Tennis has no data for a match.
type KalshiScore struct {
	EventTicker     string `gorm:"primaryKey;column:event_ticker"`
	MilestoneID     string `gorm:"column:milestone_id"`
	Status          string `gorm:"column:status"` // "started", "interrupted", "finished", etc.
	SetsHome        int    `gorm:"column:sets_home"`
	SetsAway        int    `gorm:"column:sets_away"`
	GamesHome       int    `gorm:"column:games_home"`
	GamesAway       int    `gorm:"column:games_away"`
	PointsHome      int    `gorm:"column:points_home"`
	PointsAway      int    `gorm:"column:points_away"`
	Server          int    `gorm:"column:server"`
	CompletedRounds int    `gorm:"column:completed_rounds"`
	UpdatedTS       int64  `gorm:"column:updated_ts"`
	Payload         string `gorm:"column:payload"`
}

// ScanRun maps a row from the scan_runs audit log table.
type ScanRun struct {
	ID           int64  `gorm:"primaryKey;autoIncrement;column:id"`
	RunTS        int64  `gorm:"column:run_ts"`
	SeriesTicker string `gorm:"column:series_ticker"`
	EventsFound  int    `gorm:"column:events_found"`
	MarketsFound int    `gorm:"column:markets_found"`
	NewEvents    int    `gorm:"column:new_events"`
	NewMarkets   int    `gorm:"column:new_markets"`
}

// FiredEvent maps a row from the fired_events table.
type FiredEvent struct {
	EventTicker string `gorm:"primaryKey;column:event_ticker"`
	Strategy    string `gorm:"primaryKey;column:strategy"`
	FiredTS     int64  `gorm:"column:fired_ts"`
}

// FlashscoreMatch maps a row from the flashscore_matches table.
// Created externally — not part of schemaDDL. AutoMigrate ensures it exists.
type FlashscoreMatch struct {
	EventTicker string `gorm:"primaryKey;column:event_ticker"`
	Surface     string `gorm:"column:surface"`
}

func (FlashscoreMatch) TableName() string { return "flashscore_matches" }

// PriceBandResultRow is one row in the price_band_results table.
// Populated by the cron goroutine that computes fixed-band aggregates
// per day per strategy.
type PriceBandResultRow struct {
	ID        int64   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	RunTS     int64   `gorm:"column:run_ts" json:"run_ts"`
	Day       string  `gorm:"column:day;index:idx_pricebands_day" json:"day"`
	Strategy  string  `gorm:"column:strategy" json:"strategy"`
	BandLabel string  `gorm:"column:band_label" json:"band_label"`
	BandLo    float64 `gorm:"column:band_lo" json:"band_lo"`
	BandHi    float64 `gorm:"column:band_hi" json:"band_hi"`
	N         int     `gorm:"column:n" json:"n"`
	Wins      int     `gorm:"column:wins" json:"wins"`
	WinRate   float64 `gorm:"column:win_rate" json:"win_rate"`
	NetPnL    float64 `gorm:"column:net_pnl" json:"net_pnl"`
	Invested  float64 `gorm:"column:invested" json:"invested"`
	ROI       float64 `gorm:"column:roi" json:"roi"`
	AvgEdge   float64 `gorm:"column:avg_edge" json:"avg_edge"`
}

func (PriceBandResultRow) TableName() string { return "price_band_results" }

// BacktestResultRow persists a single strategy's backtest result.
// One row per strategy — summary + orders + cumulative P&L series stored as JSON.
type BacktestResultRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Strategy    string `gorm:"uniqueIndex;column:strategy" json:"strategy"`
	RunTS       int64  `gorm:"column:run_ts" json:"run_ts"`
	MatchCount  int    `gorm:"column:match_count" json:"match_count"`
	SummaryJSON string `gorm:"column:summary_json" json:"summary_json"`
	OrdersJSON  string `gorm:"column:orders_json" json:"orders_json"`
	CumPnLJSON  string `gorm:"column:cum_pnl_json" json:"cum_pnl_json"`
	UpdatedAt   int64  `gorm:"column:updated_at" json:"updated_at"`
}

func (BacktestResultRow) TableName() string { return "backtest_results" }

// SimulationInsightRow is one row in simulation_insights.
// Per strategy × day × fixed price band, with derived metrics + peak flag.
// Populated by the pricebands cron. Read by /api/simulation endpoint.
type SimulationInsightRow struct {
	ID           int64   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	RunTS        int64   `gorm:"column:run_ts" json:"run_ts"`
	Day          string  `gorm:"column:day;index:idx_sim_insights_day" json:"day"`
	Strategy     string  `gorm:"column:strategy;index:idx_sim_insights_strategy" json:"strategy"`
	BandLabel    string  `gorm:"column:band_label" json:"band_label"`
	BandLo       float64 `gorm:"column:band_lo" json:"band_lo"`
	BandHi       float64 `gorm:"column:band_hi" json:"band_hi"`
	N            int     `gorm:"column:n" json:"n"`
	Wins         int     `gorm:"column:wins" json:"wins"`
	WinRate      float64 `gorm:"column:win_rate" json:"win_rate"`
	NetPnL       float64 `gorm:"column:net_pnl" json:"net_pnl"`
	Invested     float64 `gorm:"column:invested" json:"invested"`
	ROI          float64 `gorm:"column:roi" json:"roi"`
	AvgEdge      float64 `gorm:"column:avg_edge" json:"avg_edge"`
	Sharpe       float64 `gorm:"column:sharpe" json:"sharpe"`
	ProfitFactor float64 `gorm:"column:profit_factor" json:"profit_factor"`
	MaxDrawdown  float64 `gorm:"column:max_drawdown" json:"max_drawdown"`
	Score        float64 `gorm:"column:score" json:"score"`
	Peak         bool    `gorm:"column:peak" json:"peak"`
}

func (SimulationInsightRow) TableName() string { return "simulation_insights" }

// PaperOrderInsightRow is one row in paper_order_insights.
// Per strategy × day × fixed price band, derived from live orders table
// (is_real = false, market resolved). Populated by paperorderinsights cron.
type PaperOrderInsightRow struct {
	ID           int64   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	RunTS        int64   `gorm:"column:run_ts" json:"run_ts"`
	Day          string  `gorm:"column:day;index:idx_paper_insights_day" json:"day"`
	Strategy     string  `gorm:"column:strategy;index:idx_paper_insights_strategy" json:"strategy"`
	BandLabel    string  `gorm:"column:band_label" json:"band_label"`
	BandLo       float64 `gorm:"column:band_lo" json:"band_lo"`
	BandHi       float64 `gorm:"column:band_hi" json:"band_hi"`
	N            int     `gorm:"column:n" json:"n"`
	Wins         int     `gorm:"column:wins" json:"wins"`
	WinRate      float64 `gorm:"column:win_rate" json:"win_rate"`
	NetPnL       float64 `gorm:"column:net_pnl" json:"net_pnl"`
	Invested     float64 `gorm:"column:invested" json:"invested"`
	ROI          float64 `gorm:"column:roi" json:"roi"`
	AvgEdge      float64 `gorm:"column:avg_edge" json:"avg_edge"`
	Sharpe       float64 `gorm:"column:sharpe" json:"sharpe"`
	ProfitFactor float64 `gorm:"column:profit_factor" json:"profit_factor"`
	MaxDrawdown  float64 `gorm:"column:max_drawdown" json:"max_drawdown"`
	Score        float64 `gorm:"column:score" json:"score"`
	Peak         bool    `gorm:"column:peak" json:"peak"`
}

func (PaperOrderInsightRow) TableName() string { return "paper_order_insights" }

// PaperOrderSummaryRow is one row in paper_order_summaries.
// Per-strategy aggregate + cumulative P&L series for live paper orders.
type PaperOrderSummaryRow struct {
	ID            int64   `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Strategy      string  `gorm:"uniqueIndex;column:strategy" json:"strategy"`
	RunTS         int64   `gorm:"column:run_ts" json:"run_ts"`
	TotalSignals  int     `gorm:"column:total_signals" json:"total_signals"`
	Wins          int     `gorm:"column:wins" json:"wins"`
	Losses        int     `gorm:"column:losses" json:"losses"`
	WinRate       float64 `gorm:"column:win_rate" json:"win_rate"`
	TotalInvested float64 `gorm:"column:total_invested" json:"total_invested"`
	NetPnL        float64 `gorm:"column:net_pnl" json:"net_pnl"`
	ROI           float64 `gorm:"column:roi" json:"roi"`
	AvgEdge       float64 `gorm:"column:avg_edge" json:"avg_edge"`
	Sharpe        float64 `gorm:"column:sharpe" json:"sharpe"`
	ProfitFactor  float64 `gorm:"column:profit_factor" json:"profit_factor"`
	MaxDrawdown   float64 `gorm:"column:max_drawdown" json:"max_drawdown"`
	CumPnLJSON    string  `gorm:"column:cum_pnl_json" json:"cum_pnl_json"`
}

func (PaperOrderSummaryRow) TableName() string { return "paper_order_summaries" }

// SchemaMigration tracks applied SQL migrations.
type SchemaMigration struct {
	Name      string `gorm:"primaryKey;column:name"`
	AppliedAt int64  `gorm:"column:applied_at"`
}

func (SchemaMigration) TableName() string { return "schema_migrations" }
