package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// FadeLongshotConfig controls the fade-longshot strategy.
// Data: at T-10min, favorite with price >= 50c won 100% (n=71, +10.4c edge).
// At T-10min, price >= 85c: 100% hit, +4.5c edge, Sharpe 1.01.
type FadeLongshotConfig struct {
	// WindowSeconds: how many seconds before close to enter.
	WindowSeconds int
	// MinPrice: minimum favorite price to enter (0 = no filter).
	MinPrice float64
	// MaxPrice: maximum favorite price to enter (0 = no cap).
	MaxPrice float64
	// BaseSize: base order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
	// DynamicConvProb: if true, convProb is derived from live score context
	// instead of using fixed 0.99. Requires point data.
	DynamicConvProb bool
}

func DefaultFadeLongshotConfig() FadeLongshotConfig {
	return FadeLongshotConfig{
		WindowSeconds:   900,
		MinPrice:        0.50,
		MaxPrice:        0.0,
		BaseSize:        10.0,
		Label:           "fadelongshot",
		DynamicConvProb: true,
	}
}

// FadeLongshotStrategy buys the favorite (higher-priced YES) at a fixed
// time before market close. Data shows favorites win 100% in sample
// with +10c edge at T-10min.
//
// This strategy needs close_ts for each event, provided via
// RegisterCloseTime. In live mode, close_ts comes from the markets table.
// In backtest, the backtest engine provides it.
type FadeLongshotStrategy struct {
	mu            sync.RWMutex
	prices        map[string]float64
	priceTimes    map[string]time.Time
	markets       map[string][]string
	closeTimes    map[string]int64
	fired         map[string]bool // event_ticker -> fired
	closeWarned   map[string]bool // warn once per event when close_ts=0
	emitter       OrderEmitter
	db            *store.DB // nil in backtest mode
	log           *slog.Logger
	cfg           FadeLongshotConfig
	bankroll      float64
	kellyFraction float64
	replayNow     *time.Time

	// Live score state from OnPoints
	scores map[string]*matchScore // event_ticker -> score
}

func NewFadeLongshotStrategy(emitter OrderEmitter, log *slog.Logger, cfg FadeLongshotConfig, bankroll, kellyFraction float64) *FadeLongshotStrategy {
	return &FadeLongshotStrategy{
		prices:        make(map[string]float64),
		priceTimes:    make(map[string]time.Time),
		markets:       make(map[string][]string),
		closeTimes:    make(map[string]int64),
		fired:         make(map[string]bool),
		closeWarned:   make(map[string]bool),
		scores:        make(map[string]*matchScore),
		emitter:       emitter,
		log:           log,
		cfg:           cfg,
		bankroll:      bankroll,
		kellyFraction: kellyFraction,
	}
}

// NewFadeLongshotStrategyWithDB creates a live-mode fadelongshot that
// auto-loads close_ts from the markets table on RegisterMarkets.
// Also loads persisted fired state so restarts don't cause duplicate orders.
func NewFadeLongshotStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg FadeLongshotConfig, bankroll, kellyFraction float64) *FadeLongshotStrategy {
	s := NewFadeLongshotStrategy(emitter, log, cfg, bankroll, kellyFraction)
	s.db = db
	if fired, err := db.LoadFiredEvents(context.Background(), cfg.Label); err == nil {
		s.fired = fired
		log.Info("fadelongshot: loaded fired state from DB", "count", len(fired))
	} else {
		log.Error("fadelongshot: load fired state", "err", err)
	}
	return s
}

func (s *FadeLongshotStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()

	// Live mode: auto-load close_ts from DB so we don't need external wiring
	if s.db != nil {
		s.loadCloseTime(eventTicker)
	}
}

// loadCloseTime queries close_ts from the markets table. Called on
// RegisterMarkets in live mode. In backtest, RegisterCloseTime is used instead.
func (s *FadeLongshotStrategy) loadCloseTime(eventTicker string) {
	mkts, err := s.db.GetMarketsByEvent(context.Background(), eventTicker)
	if err != nil {
		s.log.Error("fadelongshot: load close_ts", "event", eventTicker, "err", err)
		return
	}
	for _, m := range mkts {
		if m.CloseTS > 0 {
			s.mu.Lock()
			s.closeTimes[eventTicker] = m.CloseTS
			delete(s.closeWarned, eventTicker)
			s.mu.Unlock()
			s.log.Info("fadelongshot: loaded close_ts", "event", eventTicker, "close_ts", m.CloseTS)
			return
		}
	}
	// No close_ts yet — warn once per event
	s.mu.Lock()
	if !s.closeWarned[eventTicker] {
		s.closeWarned[eventTicker] = true
		s.mu.Unlock()
		s.log.Warn("fadelongshot: no close_ts for event", "event", eventTicker)
		return
	}
	s.mu.Unlock()
}

// RegisterCloseTime sets the close timestamp for an event.
// closeTs is unix milliseconds.
func (s *FadeLongshotStrategy) RegisterCloseTime(eventTicker string, closeTs int64) {
	s.mu.Lock()
	s.closeTimes[eventTicker] = closeTs
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.closeTimes, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.closeWarned, eventTicker)
	delete(s.scores, eventTicker)
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
	s.checkEntry(marketTicker)
}

func (s *FadeLongshotStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
	s.checkEntryAt(marketTicker, ts)
}

func (s *FadeLongshotStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

// matchScore tracks the latest known score for an event from point data.
// Used to compute dynamic conversion probability.
type matchScore struct {
	setNumber    int
	homeGames    int // games in current set
	awayGames    int
	homeSetWins  int // sets won
	awaySetWins  int
	server       int // 1=home, 2=away
	isMatchPoint bool
	isSetPoint   bool
}

func (s *FadeLongshotStrategy) OnPoints(pts []store.Point) {
	for _, p := range pts {
		s.mu.Lock()
		ms, ok := s.scores[p.MatchTicker]
		if !ok {
			ms = &matchScore{}
			s.scores[p.MatchTicker] = ms
		}
		ms.setNumber = p.SetNumber
		ms.homeGames = p.HomeGames
		ms.awayGames = p.AwayGames
		ms.server = p.Server
		ms.isMatchPoint = p.IsMatchPoint
		ms.isSetPoint = p.IsSetPoint
		// Derive set wins from set number + game leader
		if p.SetNumber > 0 {
			if p.HomeGames > p.AwayGames {
				ms.homeSetWins = p.SetNumber - 1
				ms.awaySetWins = 0
			} else if p.AwayGames > p.HomeGames {
				ms.awaySetWins = p.SetNumber - 1
				ms.homeSetWins = 0
			}
		}
		s.mu.Unlock()
	}
	// Re-check entry after score update — convProb may have changed
	for _, p := range pts {
		s.mu.RLock()
		mkts, ok := s.markets[p.MatchTicker]
		s.mu.RUnlock()
		if !ok {
			continue
		}
		for _, mkt := range mkts {
			s.mu.RLock()
			pr := s.prices[mkt]
			s.mu.RUnlock()
			if pr > 0 {
				s.checkEntry(mkt)
			}
		}
	}
}

// dynamicConvProb estimates conversion probability from live score context.
// Higher when favorite has set/game lead, serving, or at match/set point.
func (s *FadeLongshotStrategy) dynamicConvProb(eventTicker string, favPrice float64) float64 {
	s.mu.RLock()
	ms, ok := s.scores[eventTicker]
	s.mu.RUnlock()
	if !ok || ms == nil {
		// No score data — fall back to price-implied probability with small edge
		return favPrice + 0.02
	}

	prob := 0.90 // base for favorite in final minutes

	// Set lead: +3c per set lead
	setLead := ms.homeSetWins - ms.awaySetWins
	if setLead < 0 {
		setLead = -setLead
	}
	prob += float64(setLead) * 0.03

	// Game lead in current set: +1c per game
	gameLead := ms.homeGames - ms.awayGames
	if gameLead < 0 {
		gameLead = -gameLead
	}
	prob += float64(gameLead) * 0.01

	// Match point: near-certain conversion
	if ms.isMatchPoint {
		prob = 0.995
	}

	// Set point: high conversion
	if ms.isSetPoint && !ms.isMatchPoint {
		prob = math.Max(prob, 0.97)
	}

	// Clamp: must stay above favPrice to have edge, cap at 0.999
	if prob <= favPrice {
		prob = favPrice + 0.01
	}
	if prob > 0.999 {
		prob = 0.999
	}

	return prob
}

func (s *FadeLongshotStrategy) checkEntry(marketTicker string) {
	s.checkEntryAt(marketTicker, s.now())
}

func (s *FadeLongshotStrategy) checkEntryAt(marketTicker string, ts time.Time) {
	s.mu.Lock()

	eventTicker := ""
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				eventTicker = et
				break
			}
		}
	}
	if eventTicker == "" {
		s.mu.Unlock()
		return
	}

	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	closeTs, ok := s.closeTimes[eventTicker]
	if !ok || closeTs == 0 {
		s.mu.Unlock()
		// Retry: close_ts may have arrived via lifecycle event after RegisterMarkets
		if s.db != nil {
			s.loadCloseTime(eventTicker)
		}
		return
	}

	entryWindow := int64(s.cfg.WindowSeconds) * 1000
	entryTs := closeTs - entryWindow
	if ts.UnixMilli() < entryTs {
		s.mu.Unlock()
		return
	}

	mkts := s.markets[eventTicker]
	if len(mkts) < 2 {
		s.mu.Unlock()
		return
	}

	price := s.prices[marketTicker]
	if price <= 0 {
		s.mu.Unlock()
		return
	}

	otherMkt := ""
	otherPrice := 0.0
	for _, m := range mkts {
		if m != marketTicker {
			otherMkt = m
			otherPrice = s.prices[m]
			break
		}
	}

	if otherPrice <= 0 {
		s.mu.Unlock()
		return
	}

	favMkt := marketTicker
	favPrice := price
	if otherPrice > price {
		favMkt = otherMkt
		favPrice = otherPrice
	}

	if favPrice < s.cfg.MinPrice {
		s.mu.Unlock()
		return
	}
	if s.cfg.MaxPrice > 0 && favPrice > s.cfg.MaxPrice {
		s.mu.Unlock()
		return
	}

	s.fired[eventTicker] = true
	s.mu.Unlock()

	// Persist fired state so restart doesn't re-fire
	if s.db != nil {
		if err := s.db.MarkFired(context.Background(), eventTicker, s.cfg.Label); err != nil {
			s.log.Error("fadelongshot: persist fired state", "event", eventTicker, "err", err)
		}
	}

	convProb := 0.99
	if s.cfg.DynamicConvProb {
		convProb = s.dynamicConvProb(eventTicker, favPrice)
	}
	edgeCents := int((convProb-favPrice)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	size := kellySize(convProb, favPrice, s.bankroll, s.kellyFraction)
	if size <= 0 {
		size = s.cfg.BaseSize
	}

	payload, _ := json.Marshal(map[string]any{
		"window_s":    s.cfg.WindowSeconds,
		"close_ts":    closeTs,
		"entry_ts":    ts.UnixMilli(),
		"fav_price":   favPrice,
		"other_price": otherPrice,
		"conv_prob":   convProb,
		"dynamic":     s.cfg.DynamicConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  favMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("fade_longshot_T-%ds", s.cfg.WindowSeconds),
		ConvProb:      convProb,
		MarketPrice:   favPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     0,
		Strategy:      "fadelongshot",
		Payload:       string(payload),
		Bankroll:      s.bankroll,
		KellyFraction: s.kellyFraction,
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("fadelongshot: order dropped", "match", eventTicker, "market", favMkt)
		return
	}
	s.log.Info("fadelongshot: order emitted",
		"match", eventTicker, "market", favMkt,
		"price", favPrice, "edge_cents", edgeCents, "size", size)
}

func (s *FadeLongshotStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *FadeLongshotStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *FadeLongshotStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("FadeLongshotStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}
