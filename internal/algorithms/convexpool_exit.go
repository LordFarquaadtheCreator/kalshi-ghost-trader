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

// ConvexPoolExitStrategy extends ConvexPoolStrategy with a full
// sell-to-close pipeline. Entry logic is identical to ConvexPoolStrategy
// (Markov fair value blended with market price via convex combination).
//
// Exit conditions (checked on every OnPrice AND OnPoint after entry):
//   - Take profit: price rises TakeProfitCents above entry
//   - Stop loss: price drops StopLossCents below entry
//   - Time exit: MaxHoldSeconds elapsed since entry
//   - Edge reversal: recomputed blended edge drops below ExitEdgeCents
//     (model no longer sees positive edge — may go negative)
//
// One open position per market. No stacking. After exit, can re-enter
// on the next point that passes entry filters.
//
// Implements ScoreObserver (entry on OnPoint, edge-reversal exit on OnPoint).
// Implements PreMatchGated — only fires after match starts.
type ConvexPoolExitStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64
	priceTimes map[string]time.Time
	markets    map[string][]string
	states     map[string]*cpMatchState
	series     map[string]string
	fvCache    map[string]float64 // market_ticker -> last Markov fair value
	positions  map[string]*cpePosition
	emitter    OrderEmitter
	db         *store.DB
	model      *MarkovModel
	cfg        ConvexPoolExitConfig
	log        *slog.Logger
	replayNow  *time.Time
}

// cpePosition tracks an open convexpool-exit position awaiting exit.
type cpePosition struct {
	MarketTicker string
	EntryPrice   float64
	EntryEdge    float64 // blended - price at entry (dollars)
	EntryTS      int64   // unix ms
	BuySize      float64
}

// ConvexPoolExitConfig configures the convex pool exit strategy.
type ConvexPoolExitConfig struct {
	PServe         float64
	Alpha          float64 // model weight (0-1). 0.5 = equal blend
	MinEdgeCents   int     // minimum edge to trigger entry
	MinMarketPrice float64
	MaxMarketPrice float64
	Label          string
	SeriesFilter   []string
	UTCHourStart   int
	UTCHourEnd     int
	// Exit params
	TakeProfitCents int // exit if price rises this many cents above entry
	StopLossCents   int // exit if price drops this many cents below entry
	MaxHoldSeconds  int // time exit
	ExitEdgeCents   int // exit if recomputed edge drops below this (can be negative)
}

// DefaultConvexPoolExitConfig returns sensible defaults.
func DefaultConvexPoolExitConfig() ConvexPoolExitConfig {
	return ConvexPoolExitConfig{
		PServe:          0.64,
		Alpha:           0.5,
		MinEdgeCents:    3,
		MinMarketPrice:  0.05,
		MaxMarketPrice:  0.95,
		Label:           "convexpool-exit",
		TakeProfitCents: 5,
		StopLossCents:   5,
		MaxHoldSeconds:  300,
		ExitEdgeCents:   0, // exit when edge vanishes
	}
}

// NewConvexPoolExitStrategy creates a convexpool-exit strategy.
func NewConvexPoolExitStrategy(emitter OrderEmitter, log *slog.Logger, cfg ConvexPoolExitConfig) *ConvexPoolExitStrategy {
	return &ConvexPoolExitStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		states:     make(map[string]*cpMatchState),
		series:     make(map[string]string),
		fvCache:    make(map[string]float64),
		positions:  make(map[string]*cpePosition),
		emitter:    emitter,
		model:      NewMarkovModelWithProb(cfg.PServe),
		cfg:        cfg,
		log:        log,
	}
}

// NewConvexPoolExitStrategyWithDB creates a live-mode convexpool-exit.
func NewConvexPoolExitStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg ConvexPoolExitConfig) *ConvexPoolExitStrategy {
	s := NewConvexPoolExitStrategy(emitter, log, cfg)
	s.db = db
	return s
}

// SetSharedMarkovModel replaces the per-strategy model with a shared one.
func (s *ConvexPoolExitStrategy) SetSharedMarkovModel(m *MarkovModel) {
	s.model = m
}

// SetSeriesTicker maps event_ticker to series_ticker for series filtering.
func (s *ConvexPoolExitStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

func (s *ConvexPoolExitStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *ConvexPoolExitStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	pos := s.positions[marketTicker]
	s.mu.Unlock()

	if pos != nil {
		s.checkExit(marketTicker, price, ts, pos, "price")
	}
}

// SetReplayTime sets the virtual "now" for staleness checks in backtest mode.
func (s *ConvexPoolExitStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *ConvexPoolExitStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *ConvexPoolExitStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	if _, ok := s.states[eventTicker]; !ok {
		s.states[eventTicker] = &cpMatchState{}
	}
	s.mu.Unlock()

	if s.db != nil {
		s.loadSeriesTicker(eventTicker)
	}
}

func (s *ConvexPoolExitStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
}

func (s *ConvexPoolExitStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
		delete(s.fvCache, mkt)
		delete(s.positions, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.states, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *ConvexPoolExitStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *ConvexPoolExitStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)
	s.processPoint(eventTicker, p)
}

func (s *ConvexPoolExitStrategy) updateMatchState(eventTicker string, p store.Point) {
	s.mu.Lock()
	ms := s.states[eventTicker]
	if ms == nil {
		ms = &cpMatchState{}
		s.states[eventTicker] = ms
	}
	if p.HomeSetGames > ms.setsHome {
		ms.setsHome = p.HomeSetGames
	}
	if p.AwaySetGames > ms.setsAway {
		ms.setsAway = p.AwaySetGames
	}
	s.mu.Unlock()
}

func (s *ConvexPoolExitStrategy) processPoint(eventTicker string, p store.Point) {
	s.mu.RLock()
	mkts, ok := s.markets[eventTicker]
	series := s.series[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mkts) < 2 {
		return
	}

	if !seriesMatches(series, s.cfg.SeriesFilter) {
		return
	}

	if !utcHourMatches(s.now(), s.cfg.UTCHourStart, s.cfg.UTCHourEnd) {
		return
	}

	setsHome := s.getSetsHome(eventTicker)
	setsAway := s.getSetsAway(eventTicker)

	fvHome := s.model.FairValue(
		setsHome, setsAway,
		p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints,
		p.Server, p.IsTiebreak,
	)
	fvAway := 1.0 - fvHome

	now := s.now()

	for i, mkt := range mkts {
		fv := fvHome
		if i == 1 {
			fv = fvAway
		}

		// Cache fair value for edge-reversal checks on OnPrice.
		s.mu.Lock()
		s.fvCache[mkt] = fv
		s.mu.Unlock()

		s.mu.RLock()
		price := s.prices[mkt]
		priceTime := s.priceTimes[mkt]
		pos := s.positions[mkt]
		s.mu.RUnlock()

		if price <= 0 || now.Sub(priceTime) > priceStaleTTL {
			continue
		}

		// If position open, check edge-reversal exit using fresh fv.
		if pos != nil {
			blended := s.cfg.Alpha*fv + (1-s.cfg.Alpha)*price
			edgeCents := int((blended - price) * 100)
			if edgeCents < s.cfg.ExitEdgeCents {
				s.checkExit(mkt, price, now, pos, "edge_reversal")
			}
			continue // don't enter while position open
		}

		// No position — check entry.
		blended := s.cfg.Alpha*fv + (1-s.cfg.Alpha)*price
		edgeCents := int((blended - price) * 100)

		if edgeCents < s.cfg.MinEdgeCents {
			continue
		}
		if price < s.cfg.MinMarketPrice {
			continue
		}
		if s.cfg.MaxMarketPrice > 0 && price > s.cfg.MaxMarketPrice {
			continue
		}

		size := kellySized(blended, price)
		if size < 1 {
			size = 1
		}

		payload, _ := json.Marshal(map[string]any{
			"fv":          fv,
			"blended":     blended,
			"entry_price": price,
			"edge_cents":  edgeCents,
			"alpha":       s.cfg.Alpha,
			"tp_price":    price + float64(s.cfg.TakeProfitCents)/100.0,
			"sl_price":    price - float64(s.cfg.StopLossCents)/100.0,
			"max_hold_s":  s.cfg.MaxHoldSeconds,
			"entry_ts":    now.UnixMilli(),
		})

		o := store.Order{
			TS:            now.UnixMilli(),
			MatchTicker:   eventTicker,
			MarketTicker:  mkt,
			Action:        "buy",
			Side:          store.OrderSideOpen,
			Context:       fmt.Sprintf("cpe_entry_%s_set%d_game%d_pt%d", sideName(i+1), p.SetNumber, p.GameNumber, p.PointNumber),
			ConvProb:      blended,
			MarketPrice:   price,
			EdgeCents:     edgeCents,
			SuggestedSize: size,
			Bankroll:      paperBankroll,
			KellyFraction: kellyFractionP,
			SetNumber:     p.SetNumber,
			Strategy:      s.cfg.Label,
			Payload:       string(payload),
		}

		// Record position before emitting to avoid re-entry.
		s.mu.Lock()
		s.positions[mkt] = &cpePosition{
			MarketTicker: mkt,
			EntryPrice:   price,
			EntryEdge:    blended - price,
			EntryTS:      now.UnixMilli(),
			BuySize:      size,
		}
		s.mu.Unlock()

		if !s.emitter.EmitOrder(o) {
			s.mu.Lock()
			delete(s.positions, mkt)
			s.mu.Unlock()
			s.log.Warn("convexpool-exit: buy dropped",
				"match", eventTicker, "market", mkt)
			continue
		}

		s.log.Debug("convexpool-exit: buy emitted",
			"event", eventTicker, "market", mkt,
			"fv", fv, "blended", blended, "price", price, "edge", edgeCents)
	}
}

// checkExit evaluates TP/SL/time/edge-reversal exit. source is "price" or
// "edge_reversal" — edge_reversal skips re-checking edge (already decided).
func (s *ConvexPoolExitStrategy) checkExit(marketTicker string, price float64, ts time.Time, pos *cpePosition, source string) {
	tpPrice := pos.EntryPrice + float64(s.cfg.TakeProfitCents)/100.0
	slPrice := pos.EntryPrice - float64(s.cfg.StopLossCents)/100.0
	maxHoldMs := int64(s.cfg.MaxHoldSeconds) * 1000

	reason := ""
	sellPrice := 0.0

	switch {
	case source == "edge_reversal":
		reason = "edge"
		sellPrice = price
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

	edgeCents := int((sellPrice - pos.EntryPrice) * 100)

	payload, _ := json.Marshal(map[string]any{
		"entry_price": pos.EntryPrice,
		"exit_price":  sellPrice,
		"exit_reason": reason,
		"entry_edge":  pos.EntryEdge,
		"hold_ms":     ts.UnixMilli() - pos.EntryTS,
		"pnl_cents":   edgeCents,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "sell",
		Side:          store.OrderSideClose,
		Context:       fmt.Sprintf("cpe_sell_%s", reason),
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
		s.log.Warn("convexpool-exit: sell dropped",
			"match", eventTicker, "market", marketTicker, "reason", reason)
		s.mu.Lock()
		s.positions[marketTicker] = pos
		s.mu.Unlock()
		return
	}

	s.log.Debug("convexpool-exit: sell emitted",
		"match", eventTicker, "market", marketTicker,
		"reason", reason, "entry", pos.EntryPrice,
		"exit", sellPrice, "pnl_cents", edgeCents,
		"hold_s", (ts.UnixMilli()-pos.EntryTS)/1000)
}

func (s *ConvexPoolExitStrategy) getSetsHome(eventTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms := s.states[eventTicker]; ms != nil {
		return ms.setsHome
	}
	return 0
}

func (s *ConvexPoolExitStrategy) getSetsAway(eventTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms := s.states[eventTicker]; ms != nil {
		return ms.setsAway
	}
	return 0
}

func (s *ConvexPoolExitStrategy) eventForMarketLocked(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *ConvexPoolExitStrategy) String() string {
	return fmt.Sprintf("ConvexPoolExitStrategy{markets=%d, positions=%d}", len(s.markets), len(s.positions))
}

func (s *ConvexPoolExitStrategy) PreMatchGated() {}

func (s *ConvexPoolExitStrategy) OnTick(ctx context.Context) {}
