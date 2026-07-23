package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// BookTick is a timestamped orderbook snapshot from ticker messages.
// Used by BookPressureStrategy to compute bid/ask size imbalance.
type BookTick struct {
	TS       int64
	Bid      float64
	Ask      float64
	BidSize  float64
	AskSize  float64
}

// BookPressureConfig controls the book-pressure microstructure strategy.
//
// Research (sampled 200K ticker msgs across finalized markets):
//   - Strong bid pressure (>0.6): +0.98c avg drift over 120s, 42.6% up rate
//   - Strong ask pressure (<-0.6): -0.33c avg drift over 120s, 30.7% up rate
//   - Signal works across all price levels (0.20-0.80)
//   - Signal is asymmetric — bid side much stronger than ask side
//   - Avg pressure during match does NOT predict final outcome
//
// Entry: pressure = (bid_size - ask_size) / (bid_size + ask_size) > MinPressure
// Buy YES at current price. Hold HoldSeconds, then sell at market price.
// Exit early if pressure reverses below ExitPressure.
type BookPressureConfig struct {
	// MinPressure: threshold to trigger entry (default 0.60)
	MinPressure float64
	// ExitPressure: exit if pressure drops below this (default 0.0)
	ExitPressure float64
	// MinBidSize: minimum bid size to consider signal valid (default 100)
	MinBidSize float64
	// MinAskSize: minimum ask size to consider signal valid (default 100)
	MinAskSize float64
	// MinEntryPrice: don't buy below (default 0.15)
	MinEntryPrice float64
	// MaxEntryPrice: don't buy above (default 0.85)
	MaxEntryPrice float64
	// HoldSeconds: time-based exit (default 120)
	HoldSeconds int
	// TakeProfitCents: exit if price rises this many cents (default 5)
	TakeProfitCents int
	// StopLossCents: exit if price drops this many cents (default 3)
	StopLossCents int
	// CooldownSeconds: min seconds between entries on same market (default 60)
	CooldownSeconds int
	// BaseSize: order size in dollars
	BaseSize float64
	// Label: strategy label
	Label string
}

func DefaultBookPressureConfig() BookPressureConfig {
	return BookPressureConfig{
		MinPressure:     0.60,
		ExitPressure:    0.0,
		MinBidSize:      100,
		MinAskSize:      100,
		MinEntryPrice:   0.15,
		MaxEntryPrice:   0.85,
		HoldSeconds:     120,
		TakeProfitCents: 5,
		StopLossCents:   3,
		CooldownSeconds: 60,
		BaseSize:        10.0,
		Label:           "bookpressure",
	}
}

// bpPosition tracks an open book-pressure position awaiting exit.
type bpPosition struct {
	MarketTicker string
	EntryPrice   float64
	EntryTS      int64
	BuySize      float64
}

// bpScoreState tracks live score context for entry filtering.
type bpScoreState struct {
	isMatchPoint bool
	isSetPoint   bool
}

// BookPressureStrategy buys YES when bid/ask size imbalance signals
// short-term price drift upward. Exits via TP, SL, time, or pressure reversal.
//
// Uses book data (bid/ask sizes) from ticker messages. In backtest,
// receives book data via BookSetter interface. In live mode, receives
// it via OnPrice (price field from ticker messages includes bid/ask).
//
// Implements ScoreObserver for match/set point context filtering.
// Implements PreMatchGated — only fires after match starts.
type BookPressureStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64          // latest price per market
	books      map[string][]BookTick       // book history per market (backtest)
	markets    map[string][]string         // event_ticker -> [home, away]
	positions  map[string]*bpPosition      // market_ticker -> open position
	cooldowns  map[string]int64            // market_ticker -> last exit ts
	scoreState map[string]*bpScoreState    // event_ticker -> score context
	emitter    OrderEmitter
	log        *slog.Logger
	cfg        BookPressureConfig
	replayNow  *time.Time
}

func NewBookPressureStrategy(emitter OrderEmitter, log *slog.Logger, cfg BookPressureConfig) *BookPressureStrategy {
	return &BookPressureStrategy{
		prices:     make(map[string]float64),
		books:      make(map[string][]BookTick),
		markets:    make(map[string][]string),
		positions:  make(map[string]*bpPosition),
		cooldowns:  make(map[string]int64),
		scoreState: make(map[string]*bpScoreState),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

func (s *BookPressureStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *BookPressureStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	mkts := s.markets[eventTicker]
	for _, mkt := range mkts {
		delete(s.prices, mkt)
		delete(s.books, mkt)
		delete(s.positions, mkt)
		delete(s.cooldowns, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.scoreState, eventTicker)
	s.mu.Unlock()
}

// SetBookSeries sets the book tick history for a market. Used by backtest
// engine to provide historical bid/ask/sizes without lookahead bias.
func (s *BookPressureStrategy) SetBookSeries(marketTicker string, books []BookTick) {
	s.mu.Lock()
	s.books[marketTicker] = books
	s.mu.Unlock()
}

// bookAtTime returns the book state at or before the given timestamp
// using binary search on the pre-loaded book series. Caller must hold s.mu.
func (s *BookPressureStrategy) bookAtTimeLocked(marketTicker string, ts int64) (BookTick, bool) {
	series := s.books[marketTicker]
	if len(series) == 0 {
		return BookTick{}, false
	}
	idx := sort.Search(len(series), func(i int) bool {
		return series[i].TS > ts
	})
	if idx == 0 {
		return BookTick{}, false
	}
	return series[idx-1], true
}

func (s *BookPressureStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *BookPressureStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price

	pos := s.positions[marketTicker]
	if pos != nil {
		s.mu.Unlock()
		s.checkExit(marketTicker, price, ts, pos)
		return
	}

	s.checkEntry(marketTicker, price, ts)
	s.mu.Unlock()
}

// pressureAtLocked computes book pressure at the given timestamp. Caller must hold s.mu.
func (s *BookPressureStrategy) pressureAtLocked(marketTicker string, ts int64) (float64, bool) {
	book, ok := s.bookAtTimeLocked(marketTicker, ts)
	if !ok {
		return 0, false
	}
	if book.BidSize < s.cfg.MinBidSize || book.AskSize < s.cfg.MinAskSize {
		return 0, false
	}
	total := book.BidSize + book.AskSize
	if total <= 0 {
		return 0, false
	}
	return (book.BidSize - book.AskSize) / total, true
}

// pressureAt computes book pressure at the given timestamp for a market.
// Acquires s.mu.RLock internally — use from contexts not already holding the lock.
func (s *BookPressureStrategy) pressureAt(marketTicker string, ts int64) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pressureAtLocked(marketTicker, ts)
}

// checkEntry evaluates book pressure and emits buy if threshold met. Called under s.mu.
func (s *BookPressureStrategy) checkEntry(marketTicker string, price float64, ts time.Time) {
	if price < s.cfg.MinEntryPrice || price > s.cfg.MaxEntryPrice {
		return
	}

	eventTicker := s.eventForMarketLocked(marketTicker)
	if eventTicker == "" {
		return
	}

	// Exclude match/set point — price moves there are informational.
	ss := s.scoreState[eventTicker]
	if ss != nil && (ss.isMatchPoint || ss.isSetPoint) {
		return
	}

	// Cooldown check
	lastExit := s.cooldowns[marketTicker]
	if lastExit > 0 && ts.UnixMilli()-lastExit < int64(s.cfg.CooldownSeconds)*1000 {
		return
	}

	// Get book pressure at current time
	tsMs := ts.UnixMilli()
	pressure, ok := s.pressureAtLocked(marketTicker, tsMs)
	if !ok {
		return
	}
	if pressure < s.cfg.MinPressure {
		return
	}

	// ConvProb: price + expected drift edge.
	// Research shows ~0.7c avg drift over 120s for strong bid pressure.
	// Edge = pressure * 2c (scales with pressure intensity).
	edgeCents := int(pressure*200 + 1e-9)
	if edgeCents < 1 {
		edgeCents = 1
	}
	convProb := price + float64(edgeCents)/100.0
	if convProb > 0.99 {
		convProb = 0.99
	}

	size := kellySized(convProb, price)
	if size < 1 {
		size = 1
	}

	payload, _ := json.Marshal(map[string]any{
		"pressure":    pressure,
		"entry_price": price,
		"edge_cents":  edgeCents,
		"conv_prob":   convProb,
		"hold_s":      s.cfg.HoldSeconds,
		"tp_cents":    s.cfg.TakeProfitCents,
		"sl_cents":    s.cfg.StopLossCents,
		"entry_ts":    tsMs,
	})

	o := store.Order{
		TS:            tsMs,
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Side:          store.OrderSideOpen,
		Context:       fmt.Sprintf("bp_%.2f_pressure", pressure),
		ConvProb:      convProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	// Record position before emitting to avoid re-entry.
	s.positions[marketTicker] = &bpPosition{
		MarketTicker: marketTicker,
		EntryPrice:   price,
		EntryTS:      tsMs,
		BuySize:      size,
	}

	emitted := s.emitter.EmitOrder(o)
	if !emitted {
		delete(s.positions, marketTicker)
		s.log.Warn("bookpressure: buy order dropped",
			"match", eventTicker, "market", marketTicker)
		return
	}

	s.log.Info("bookpressure: buy emitted",
		"match", eventTicker, "market", marketTicker,
		"price", price, "pressure", pressure,
		"edge_cents", edgeCents, "size", size)
}

// checkExit evaluates TP/SL/time/pressure-reversal exit. Called outside s.mu.
func (s *BookPressureStrategy) checkExit(marketTicker string, price float64, ts time.Time, pos *bpPosition) {
	tpPrice := pos.EntryPrice + float64(s.cfg.TakeProfitCents)/100.0
	slPrice := pos.EntryPrice - float64(s.cfg.StopLossCents)/100.0
	maxHoldMs := int64(s.cfg.HoldSeconds) * 1000

	reason := ""
	sellPrice := 0.0

	switch {
	case price >= tpPrice:
		reason = "tp"
		sellPrice = tpPrice
	case price <= slPrice:
		reason = "sl"
		sellPrice = slPrice
	case ts.UnixMilli()-pos.EntryTS >= maxHoldMs:
		reason = "time"
		sellPrice = price
	default:
		// Check pressure reversal
		pressure, ok := s.pressureAt(marketTicker, ts.UnixMilli())
		if ok && pressure < s.cfg.ExitPressure {
			reason = "reversal"
			sellPrice = price
		}
	}

	if reason == "" {
		return
	}

	s.mu.Lock()
	delete(s.positions, marketTicker)
	s.cooldowns[marketTicker] = ts.UnixMilli()
	eventTicker := s.eventForMarketLocked(marketTicker)
	s.mu.Unlock()

	if eventTicker == "" {
		return
	}

	size := pos.BuySize
	if size < 1 {
		size = 1
	}

	edgeCents := int((sellPrice - pos.EntryPrice) * 100)

	payload, _ := json.Marshal(map[string]any{
		"entry_price": pos.EntryPrice,
		"exit_price":  sellPrice,
		"exit_reason": reason,
		"hold_ms":     ts.UnixMilli() - pos.EntryTS,
		"pnl_cents":   edgeCents,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "sell",
		Side:          store.OrderSideClose,
		Context:       fmt.Sprintf("bp_sell_%s", reason),
		ConvProb:      pos.EntryPrice,
		MarketPrice:   sellPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("bookpressure: sell order dropped",
			"match", eventTicker, "market", marketTicker, "reason", reason)
		s.mu.Lock()
		s.positions[marketTicker] = pos
		s.mu.Unlock()
		return
	}

	s.log.Info("bookpressure: sell emitted",
		"match", eventTicker, "market", marketTicker,
		"reason", reason, "entry", pos.EntryPrice,
		"exit", sellPrice, "pnl_cents", edgeCents,
		"hold_s", (ts.UnixMilli()-pos.EntryTS)/1000)
}

func (s *BookPressureStrategy) OnPoint(eventTicker string, p store.Point) {
	s.mu.Lock()
	ss, ok := s.scoreState[eventTicker]
	if !ok {
		ss = &bpScoreState{}
		s.scoreState[eventTicker] = ss
	}
	ss.isMatchPoint = p.IsMatchPoint
	ss.isSetPoint = p.IsSetPoint
	s.mu.Unlock()
}

func (s *BookPressureStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *BookPressureStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *BookPressureStrategy) eventForMarketLocked(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *BookPressureStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.books, marketTicker)
	s.mu.Unlock()
}

func (s *BookPressureStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("BookPressureStrategy{%s: markets=%d, positions=%d}",
		s.cfg.Label, len(s.markets), len(s.positions))
}

func (s *BookPressureStrategy) PreMatchGated() {}

func (s *BookPressureStrategy) OnTick(ctx context.Context) {}
