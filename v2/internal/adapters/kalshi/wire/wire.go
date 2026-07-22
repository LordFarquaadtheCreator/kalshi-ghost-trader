// Package wire provides pure parsing functions for Kalshi WebSocket v2 messages.
//
// Every Kalshi WS message is a JSON envelope: {id, type, sid, seq, msg}.
// The inner msg object shape depends on type. These parsers decode the
// envelope and the common per-type payloads without any I/O or side effects.
package wire

import (
	"encoding/json"
	"fmt"
)

// Envelope is the outer wrapper of every Kalshi WS message.
type Envelope struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	SID  int64           `json:"sid"`
	Seq  int64           `json:"seq"`
	Msg  json.RawMessage `json:"msg"`
}

// TickerMsg maps the Kalshi WS ticker message (env.type == "ticker").
type TickerMsg struct {
	MarketTicker       string `json:"market_ticker"`
	MarketID           string `json:"market_id"`
	PriceDollars       string `json:"price_dollars"`
	YesBidDollars      string `json:"yes_bid_dollars"`
	YesAskDollars      string `json:"yes_ask_dollars"`
	YesBidSizeFP       string `json:"yes_bid_size_fp"`
	YesAskSizeFP       string `json:"yes_ask_size_fp"`
	VolumeFP           string `json:"volume_fp"`
	OpenInterestFP     string `json:"open_interest_fp"`
	DollarVolume       int64  `json:"dollar_volume"`
	DollarOpenInterest int64  `json:"dollar_open_interest"`
	LastTradeSizeFP    string `json:"last_trade_size_fp"`
	TsMs               int64  `json:"ts_ms"`
}

// TradeMsg maps the Kalshi WS trade message (env.type == "trade").
type TradeMsg struct {
	TradeID          string `json:"trade_id"`
	MarketTicker     string `json:"market_ticker"`
	YesPriceDollars  string `json:"yes_price_dollars"`
	NoPriceDollars   string `json:"no_price_dollars"`
	CountFP          string `json:"count_fp"`
	TakerSide        string `json:"taker_side"`
	TakerOutcomeSide string `json:"taker_outcome_side"`
	TakerBookSide    string `json:"taker_book_side"`
	TsMs             int64  `json:"ts_ms"`
}

// OrderbookMsg covers both orderbook_snapshot and orderbook_delta payloads.
// Snapshot fields (PriceDollars/DeltaFP/Side/TsMs) are zero on snapshots;
// delta fields (MarketID only on snapshot) overlap so one struct suffices.
type OrderbookMsg struct {
	MarketTicker string `json:"market_ticker"`
	MarketID     string `json:"market_id"`
	PriceDollars string `json:"price_dollars"`
	DeltaFP      string `json:"delta_fp"`
	Side         string `json:"side"`
	TsMs         int64  `json:"ts_ms"`
}

// LifecycleMsg maps the Kalshi WS market_lifecycle_v2 message.
// Timestamps are in SECONDS — callers convert to millis before storing.
type LifecycleMsg struct {
	EventType       string `json:"event_type"`
	MarketTicker    string `json:"market_ticker"`
	OpenTS          int64  `json:"open_ts"`
	CloseTS         int64  `json:"close_ts"`
	DeterminationTS int64  `json:"determination_ts"`
	SettledTS       int64  `json:"settled_ts"`
	Result          string `json:"result"`
	SettlementValue string `json:"settlement_value"`
	IsDeactivated   *bool  `json:"is_deactivated"`
}

// ParseEnvelope decodes the top-level WS message envelope.
// Returns the envelope with the raw msg payload preserved for downstream
// per-type parsing. Empty input and malformed JSON are errors.
func ParseEnvelope(data []byte) (*Envelope, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("wire: empty envelope input")
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("wire: parse envelope: %w", err)
	}
	return &env, nil
}

// ParseTicker decodes a ticker message payload (the env.msg object).
func ParseTicker(data []byte) (*TickerMsg, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("wire: empty ticker input")
	}
	var t TickerMsg
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("wire: parse ticker: %w", err)
	}
	return &t, nil
}

// ParseTrade decodes a trade message payload (the env.msg object).
func ParseTrade(data []byte) (*TradeMsg, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("wire: empty trade input")
	}
	var t TradeMsg
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("wire: parse trade: %w", err)
	}
	return &t, nil
}

// ParseOrderbook decodes an orderbook snapshot or delta payload (env.msg).
// Both share MarketTicker/MarketID; delta-only fields are zero on snapshots.
func ParseOrderbook(data []byte) (*OrderbookMsg, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("wire: empty orderbook input")
	}
	var o OrderbookMsg
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("wire: parse orderbook: %w", err)
	}
	return &o, nil
}

// ParseLifecycle decodes a market_lifecycle_v2 payload (env.msg).
func ParseLifecycle(data []byte) (*LifecycleMsg, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("wire: empty lifecycle input")
	}
	var l LifecycleMsg
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("wire: parse lifecycle: %w", err)
	}
	return &l, nil
}

// --- REST types ---

// GetEventsResponse is the response from GET /events.
type GetEventsResponse struct {
	Events []EventData `json:"events"`
	Cursor string      `json:"cursor"`
}

// EventData maps the Kalshi event object from REST.
type EventData struct {
	EventTicker       string          `json:"event_ticker"`
	SeriesTicker      string          `json:"series_ticker"`
	Title             string          `json:"title"`
	SubTitle          string          `json:"sub_title"`
	MutuallyExclusive bool            `json:"mutually_exclusive"`
	ProductMetadata   ProductMetadata `json:"product_metadata"`
}

// ProductMetadata for tennis events.
type ProductMetadata struct {
	Competition      string `json:"competition"`
	CompetitionScope string `json:"competition_scope"`
}

// GetMarketsResponse is the response from GET /markets.
type GetMarketsResponse struct {
	Markets []MarketData `json:"markets"`
	Cursor  string       `json:"cursor"`
}

// MarketData maps the Kalshi market object from REST.
type MarketData struct {
	Ticker                 string          `json:"ticker"`
	EventTicker            string          `json:"event_ticker"`
	MarketType             string          `json:"market_type"`
	YesSubTitle            string          `json:"yes_sub_title"`
	Status                 string          `json:"status"`
	OccurrenceDatetime     string          `json:"occurrence_datetime"`
	OpenTime               string          `json:"open_time"`
	CloseTime              string          `json:"close_time"`
	Result                 string          `json:"result"`
	SettlementTS           string          `json:"settlement_ts"`
	SettlementValueDollars string          `json:"settlement_value_dollars"`
	CustomStrike           json.RawMessage `json:"custom_strike"`
}
