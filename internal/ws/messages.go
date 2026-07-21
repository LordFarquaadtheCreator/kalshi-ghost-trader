package ws

import (
	"encoding/json"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// tickerMsg maps the Kalshi WS ticker message.
type tickerMsg struct {
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

func (m *Manager) handleTicker(sid int64, msg json.RawMessage, raw []byte) {
	var t tickerMsg
	if err := json.Unmarshal(msg, &t); err != nil {
		return
	}

	recvTs := time.Now().UnixMilli()
	tick := store.Tick{
		TS:                 t.TsMs,
		RecvTS:             recvTs,
		MarketTicker:       t.MarketTicker,
		MsgType:            "ticker",
		SID:                sid,
		Price:              kalshiclient.ParseFP(t.PriceDollars),
		YesBid:             kalshiclient.ParseFP(t.YesBidDollars),
		YesAsk:             kalshiclient.ParseFP(t.YesAskDollars),
		YesBidSize:         kalshiclient.ParseFP(t.YesBidSizeFP),
		YesAskSize:         kalshiclient.ParseFP(t.YesAskSizeFP),
		Volume:             kalshiclient.ParseFP(t.VolumeFP),
		OpenInterest:       kalshiclient.ParseFP(t.OpenInterestFP),
		DollarVolume:       t.DollarVolume,
		DollarOpenInterest: t.DollarOpenInterest,
		LastTradeSize:      kalshiclient.ParseFP(t.LastTradeSizeFP),
		Payload:            string(raw),
	}

	m.trackLatency(recvTs, t.TsMs)
	if !m.disableSave {
		m.tickWriter.Ingest(tick)
	}

	if m.priceUpd != nil && tick.Price > 0 {
		m.priceUpd.OnPrice(t.MarketTicker, tick.Price)
	}
}

// tradeMsg maps the Kalshi WS trade message.
type tradeMsg struct {
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

func (m *Manager) handleTrade(sid int64, msg json.RawMessage, raw []byte) {
	var t tradeMsg
	if err := json.Unmarshal(msg, &t); err != nil {
		return
	}

	recvTs := time.Now().UnixMilli()
	tick := store.Tick{
		TS:               t.TsMs,
		RecvTS:           recvTs,
		MarketTicker:     t.MarketTicker,
		MsgType:          "trade",
		SID:              sid,
		Price:            kalshiclient.ParseFP(t.YesPriceDollars),
		NoPrice:          kalshiclient.ParseFP(t.NoPriceDollars),
		Volume:           kalshiclient.ParseFP(t.CountFP),
		TradeID:          t.TradeID,
		TakerSide:        t.TakerSide,
		TakerOutcomeSide: t.TakerOutcomeSide,
		TakerBookSide:    t.TakerBookSide,
		Payload:          string(raw),
	}
	m.trackLatency(recvTs, t.TsMs)
	if !m.disableSave {
		m.tickWriter.Ingest(tick)
	}
}

// lifecycleMsg maps the Kalshi WS market_lifecycle_v2 message.
// Timestamps are in SECONDS — converted to millis before storing.
type lifecycleMsg struct {
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

func (m *Manager) handleLifecycle(msg json.RawMessage, raw []byte) {
	var lc lifecycleMsg
	if err := json.Unmarshal(msg, &lc); err != nil {
		return
	}

	// Lifecycle channel sends ALL market events (no filter supported).
	// Only store events for markets we're subscribed to.
	m.mu.Lock()
	_, tracked := m.subs[lc.MarketTicker]
	m.mu.Unlock()
	if !tracked {
		return
	}

	now := time.Now().UnixMilli()
	le := store.LifecycleEvent{
		TS:              now,
		RecvTS:          now,
		MarketTicker:    lc.MarketTicker,
		EventType:       lc.EventType,
		Result:          lc.Result,
		SettlementValue: lc.SettlementValue,
		OpenTS:          lc.OpenTS * secondsToMillis,
		CloseTS:         lc.CloseTS * secondsToMillis,
		DeterminationTS: lc.DeterminationTS * secondsToMillis,
		SettledTS:       lc.SettledTS * secondsToMillis,
		IsDeactivated:   lc.IsDeactivated,
		Payload:         string(raw),
	}
	if !m.disableSave {
		m.tickWriter.IngestLifecycle(le)
	}
}

// eventLifecycleMsg maps the Kalshi WS event_lifecycle message.
type eventLifecycleMsg struct {
	EventTicker  string `json:"event_ticker"`
	Title        string `json:"title"`
	Subtitle     string `json:"subtitle"`
	SeriesTicker string `json:"series_ticker"`
}

// handleEventLifecycle stores event creation notifications from the
// market_lifecycle_v2 channel. Filtered by configured tennis series —
// the lifecycle channel is unfiltered and delivers all Kalshi events.
func (m *Manager) handleEventLifecycle(msg json.RawMessage, raw []byte) {
	var el eventLifecycleMsg
	if err := json.Unmarshal(msg, &el); err != nil {
		return
	}
	if !m.seriesFilter[el.SeriesTicker] {
		return
	}

	now := time.Now().UnixMilli()
	if !m.disableSave {
		m.tickWriter.IngestEventLifecycle(store.EventLifecycleEvent{
			TS:           now,
			RecvTS:       now,
			EventTicker:  el.EventTicker,
			SeriesTicker: el.SeriesTicker,
			Title:        el.Title,
			Subtitle:     el.Subtitle,
			Payload:      string(raw),
		})
	}
}

// orderbookSnapshotMsg maps the Kalshi WS orderbook_snapshot message.
// No ts_ms field — use recv_ts as primary timestamp.
type orderbookSnapshotMsg struct {
	MarketTicker string `json:"market_ticker"`
	MarketID     string `json:"market_id"`
}

// orderbookDeltaMsg maps the Kalshi WS orderbook_delta message.
type orderbookDeltaMsg struct {
	MarketTicker string `json:"market_ticker"`
	MarketID     string `json:"market_id"`
	PriceDollars string `json:"price_dollars"`
	DeltaFP      string `json:"delta_fp"`
	Side         string `json:"side"`
	TsMs         int64  `json:"ts_ms"`
}

func (m *Manager) handleOrderbookSnapshot(sid, seq int64, msg json.RawMessage, raw []byte) {
	var s orderbookSnapshotMsg
	if err := json.Unmarshal(msg, &s); err != nil {
		return
	}

	now := time.Now().UnixMilli()
	if !m.disableSave {
		m.tickWriter.IngestOrderbook(store.OrderbookEvent{
			TS:           now,
			RecvTS:       now,
			MarketTicker: s.MarketTicker,
			MsgType:      "orderbook_snapshot",
			SID:          sid,
			Seq:          seq,
			Payload:      string(raw),
		})
	}
}

func (m *Manager) handleOrderbookDelta(sid, seq int64, msg json.RawMessage, raw []byte) {
	var d orderbookDeltaMsg
	if err := json.Unmarshal(msg, &d); err != nil {
		return
	}

	now := time.Now().UnixMilli()
	ts := d.TsMs
	if ts == 0 {
		ts = now
	}
	if !m.disableSave {
		m.tickWriter.IngestOrderbook(store.OrderbookEvent{
			TS:           ts,
			RecvTS:       now,
			MarketTicker: d.MarketTicker,
			MsgType:      "orderbook_delta",
			SID:          sid,
			Seq:          seq,
			Price:        kalshiclient.ParseFP(d.PriceDollars),
			Delta:        kalshiclient.ParseFP(d.DeltaFP),
			Side:         d.Side,
			Payload:      string(raw),
		})
	}
}
