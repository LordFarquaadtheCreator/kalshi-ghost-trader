package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// BuyTheDipConfig controls the buy-the-dip strategy.
//
// Research (1337 finalized markets, 4438 dips):
//   - 30c+ dips in 30s: n=125, +10.9c avg at 60s, Sharpe 0.465
//   - Winner-market dips: n=1883, +3.75c avg, Sharpe 0.327
//   - Loser-market dips: n=2555, +0.19c avg, Sharpe 0.018 (catastrophic)
//   - Best exit: TP at 75% recovery, SL at -10c, time exit at 300s
//   - Match/set point dips are informational — don't revert. Exclude.
//
// Entry: price drops >= DipThresholdCents in WindowSeconds, pre-dip price
// was > MinPreDipPrice (favourite proxy = winner market), entry price in
// [MinEntryPrice, MaxEntryPrice], NOT at match/set point.
//
// Exit (checked on every OnPrice after entry):
//   - Take profit: price recovers TakeProfitFrac of dip
//   - Stop loss: price drops StopLossCents below entry
//   - Time exit: MaxHoldSeconds elapsed
//   - Fallback: hold to settlement
type BuyTheDipConfig struct {
	// DipThresholdCents: minimum price drop in cents to trigger (default 30)
	DipThresholdCents int
	// WindowSeconds: lookback window for dip detection (default 30)
	WindowSeconds int
	// MinEntryPrice: don't buy below this price (default 0.15)
	MinEntryPrice float64
	// MaxEntryPrice: don't buy above this price (default 0.80)
	MaxEntryPrice float64
	// MinPreDipPrice: pre-dip price must exceed this — favourite proxy (default 0.50)
	MinPreDipPrice float64
	// TakeProfitFrac: fraction of dip to recover before TP exit (default 0.75)
	TakeProfitFrac float64
	// StopLossCents: exit if price drops this many cents below entry (default 10)
	StopLossCents int
	// MaxHoldSeconds: time-based exit (default 300)
	MaxHoldSeconds int
	// BaseSize: order size in dollars
	BaseSize float64
	// Label: strategy label
	Label string
}

func DefaultBuyTheDipConfig() BuyTheDipConfig {
	return BuyTheDipConfig{
		DipThresholdCents: 30,
		WindowSeconds:     30,
		MinEntryPrice:     0.15,
		MaxEntryPrice:     0.80,
		MinPreDipPrice:    0.50,
		TakeProfitFrac:    0.75,
		StopLossCents:     10,
		MaxHoldSeconds:    300,
		BaseSize:          10.0,
		Label:             "buythedip",
	}
}

// dipPriceSample is a timestamped price for dip detection.
type dipPriceSample struct {
	ts    int64
	price float64
}

// dipPosition tracks an open buy-the-dip position awaiting exit.
type dipPosition struct {
	MarketTicker string
	EntryPrice   float64
	DipSize      float64 // abs drop that triggered entry
	EntryTS      int64   // unix ms
	BuySize      float64
}

// dipScoreState tracks live score context for entry filtering.
type dipScoreState struct {
	isMatchPoint bool
	isSetPoint   bool
}

// BuyTheDipStrategy buys markets that dip sharply, then sells on
// recovery (take profit), further drop (stop loss), or time exit.
//
// First strategy with full sell-to-close pipeline. Sells flow through
// PaperPositionEmitter → position pipeline → backtest FIFO resolver.
//
// Implements ScoreObserver for match/set point context filtering.
// Implements PreMatchGated — only fires after match starts.
type BuyTheDipStrategy struct {
	mu         sync.RWMutex
	prices     map[string][]dipPriceSample // market_ticker -> sliding window
	markets    map[string][]string         // event_ticker -> [home, away]
	positions  map[string]*dipPosition     // market_ticker -> open position
	scoreState map[string]*dipScoreState   // event_ticker -> score context
	emitter    OrderEmitter
	log        *slog.Logger
	cfg        BuyTheDipConfig
	replayNow  *time.Time
}

func NewBuyTheDipStrategy(emitter OrderEmitter, log *slog.Logger, cfg BuyTheDipConfig) *BuyTheDipStrategy {
	return &BuyTheDipStrategy{
		prices:     make(map[string][]dipPriceSample),
		markets:    make(map[string][]string),
		positions:  make(map[string]*dipPosition),
		scoreState: make(map[string]*dipScoreState),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

func (s *BuyTheDipStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *BuyTheDipStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	mkts := s.markets[eventTicker]
	for _, mkt := range mkts {
		delete(s.prices, mkt)
		// Don't delete positions — open positions settle at market close.
		// But if the match is done, clear them.
		delete(s.positions, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.scoreState, eventTicker)
	s.mu.Unlock()
}

func (s *BuyTheDipStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *BuyTheDipStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()

	// Update price history, trim old samples.
	windowMs := int64(s.cfg.WindowSeconds) * 1000
	cutoff := ts.UnixMilli() - windowMs
	samples := s.prices[marketTicker]
	samples = append(samples, dipPriceSample{ts: ts.UnixMilli(), price: price})
	for len(samples) > 0 && samples[0].ts < cutoff {
		samples = samples[1:]
	}
	s.prices[marketTicker] = samples

	// Check exit first if we have an open position.
	pos := s.positions[marketTicker]
	if pos != nil {
		s.mu.Unlock()
		s.checkExit(marketTicker, price, ts, pos)
		return
	}

	// No open position — check for dip entry.
	s.checkEntry(marketTicker, price, ts, samples)
	s.mu.Unlock()
}

// checkEntry detects a dip and emits a buy order. Called under s.mu.
func (s *BuyTheDipStrategy) checkEntry(marketTicker string, price float64, ts time.Time, samples []dipPriceSample) {
	if len(samples) < 2 {
		return
	}

	// Already have a position on this market — don't stack.
	if s.positions[marketTicker] != nil {
		return
	}

	// Find event for this market.
	eventTicker := s.eventForMarketLocked(marketTicker)
	if eventTicker == "" {
		return
	}

	// Exclude match/set point context — dips there are informational.
	ss := s.scoreState[eventTicker]
	if ss != nil && (ss.isMatchPoint || ss.isSetPoint) {
		return
	}

	// Detect dip: price dropped >= threshold from window start to now.
	windowStart := samples[0].price
	dipCents := int((windowStart - price) * 100)
	if dipCents < s.cfg.DipThresholdCents {
		return
	}

	// Pre-dip price must exceed MinPreDipPrice (favourite proxy = winner market).
	if windowStart < s.cfg.MinPreDipPrice {
		return
	}

	// Entry price must be in tradeable range.
	if price < s.cfg.MinEntryPrice || price > s.cfg.MaxEntryPrice {
		return
	}

	dipSize := windowStart - price

	// ConvProb: estimate recovery probability from research.
	// Winner-market 30c+ dips recover ~53% within 300s.
	// Use pre-dip price as fair value proxy — the market was there before the dip.
	convProb := windowStart
	if convProb > 0.99 {
		convProb = 0.99
	}
	edgeCents := int((convProb-price)*100 + 1e-9)
	if edgeCents < 1 {
		edgeCents = 1
	}

	size := kellySized(convProb, price)
	if size < 1 {
		size = 1
	}

	payload, _ := json.Marshal(map[string]any{
		"pre_dip_price": windowStart,
		"entry_price":   price,
		"dip_cents":     dipCents,
		"dip_size":      dipSize,
		"conv_prob":     convProb,
		"tp_price":      price + s.cfg.TakeProfitFrac*dipSize,
		"sl_price":      price - float64(s.cfg.StopLossCents)/100.0,
		"max_hold_s":    s.cfg.MaxHoldSeconds,
		"entry_ts":      ts.UnixMilli(),
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Side:          store.OrderSideOpen,
		Context:       fmt.Sprintf("btd_dip_%dc", dipCents),
		ConvProb:      convProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	// Record position before emitting to avoid re-entry on next tick.
	s.positions[marketTicker] = &dipPosition{
		MarketTicker: marketTicker,
		EntryPrice:   price,
		DipSize:      dipSize,
		EntryTS:      ts.UnixMilli(),
		BuySize:      size,
	}

	// Emit outside lock to avoid deadlock on emitter callbacks.
	// But we're under s.mu — emitter is async-safe (channel-based), so OK.
	// If emitter blocks, it could stall. Acceptable for paper. Real emitter
	// has its own timeout.
	emitted := s.emitter.EmitOrder(o)
	if !emitted {
		delete(s.positions, marketTicker)
		s.log.Warn("buythedip: buy order dropped",
			"match", eventTicker, "market", marketTicker)
		return
	}

	s.log.Info("buythedip: buy emitted",
		"match", eventTicker, "market", marketTicker,
		"pre_dip", windowStart, "entry", price,
		"dip_cents", dipCents, "size", size,
		"tp", fmt.Sprintf("%.2f", price+s.cfg.TakeProfitFrac*dipSize),
		"sl", fmt.Sprintf("%.2f", price-float64(s.cfg.StopLossCents)/100.0))
}

// checkExit evaluates TP/SL/time exit for an open position. Called outside s.mu.
func (s *BuyTheDipStrategy) checkExit(marketTicker string, price float64, ts time.Time, pos *dipPosition) {
	tpPrice := pos.EntryPrice + s.cfg.TakeProfitFrac*pos.DipSize
	slPrice := pos.EntryPrice - float64(s.cfg.StopLossCents)/100.0
	maxHoldMs := int64(s.cfg.MaxHoldSeconds) * 1000

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
	}

	if reason == "" {
		return
	}

	// Clear position before emitting to avoid re-entry.
	s.mu.Lock()
	delete(s.positions, marketTicker)
	eventTicker := s.eventForMarketLocked(marketTicker)
	s.mu.Unlock()

	if eventTicker == "" {
		return
	}

	size := pos.BuySize
	if size < 1 {
		size = 1
	}

	edgeCents := int((sellPrice-pos.EntryPrice)*100 + 0.5)

	payload, _ := json.Marshal(map[string]any{
		"entry_price": pos.EntryPrice,
		"exit_price":  sellPrice,
		"exit_reason": reason,
		"dip_size":    pos.DipSize,
		"hold_ms":     ts.UnixMilli() - pos.EntryTS,
		"pnl_cents":   edgeCents,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "sell",
		Side:          store.OrderSideClose,
		Context:       fmt.Sprintf("btd_sell_%s", reason),
		ConvProb:      pos.EntryPrice, // fair value proxy
		MarketPrice:   sellPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("buythedip: sell order dropped",
			"match", eventTicker, "market", marketTicker, "reason", reason)
		// Re-add position so we can retry on next tick.
		s.mu.Lock()
		s.positions[marketTicker] = pos
		s.mu.Unlock()
		return
	}

	s.log.Info("buythedip: sell emitted",
		"match", eventTicker, "market", marketTicker,
		"reason", reason, "entry", pos.EntryPrice,
		"exit", sellPrice, "pnl_cents", edgeCents,
		"hold_s", (ts.UnixMilli()-pos.EntryTS)/1000)
}

func (s *BuyTheDipStrategy) OnPoint(eventTicker string, p store.Point) {
	s.mu.Lock()
	ss, ok := s.scoreState[eventTicker]
	if !ok {
		ss = &dipScoreState{}
		s.scoreState[eventTicker] = ss
	}
	ss.isMatchPoint = p.IsMatchPoint
	ss.isSetPoint = p.IsSetPoint
	s.mu.Unlock()
}

func (s *BuyTheDipStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *BuyTheDipStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *BuyTheDipStrategy) eventForMarketLocked(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *BuyTheDipStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *BuyTheDipStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("BuyTheDipStrategy{%s: markets=%d, positions=%d}",
		s.cfg.Label, len(s.markets), len(s.positions))
}

func (s *BuyTheDipStrategy) PreMatchGated() {}

func (s *BuyTheDipStrategy) OnTick(ctx context.Context) {}
