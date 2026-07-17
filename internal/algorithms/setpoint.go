package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// SetPointConfig controls set-point strategy behavior.
// Derived from data exploration (explore.py):
//   - Overall set-point conversion: 91%
//   - Serving set-point conversion: 93%
//   - Returning set-point conversion: 89%
//   - Match-point conversion (serving): 97%
//   - Match-point conversion (returning): 89%
type SetPointConfig struct {
	// IncludeSetPoints: fire on non-match-point set points (set points in
	// sets that don't decide the match, e.g. set 1 when 0-0).
	IncludeSetPoints bool
	// IncludeReturning: fire when the set-point player is returning (breaking).
	// If false, only fire when serving.
	IncludeReturning bool
	// ServeConvProb: conversion probability when serving at set point.
	ServeConvProb float64
	// ReturnConvProb: conversion probability when returning at set point.
	ReturnConvProb float64
	// MaxMarketPrice: skip signals above this price (0 = no cap).
	MaxMarketPrice float64
	// MinMarketPrice: skip signals below this price.
	MinMarketPrice float64
	// MinEdgeCents: minimum edge to emit order.
	MinEdgeCents int
	// Label: strategy name for logging.
	Label string
}

// DefaultSetPointConfig fires on all set points (serving + returning).
func DefaultSetPointConfig() SetPointConfig {
	return SetPointConfig{
		IncludeSetPoints: true,
		IncludeReturning: true,
		ServeConvProb:    0.93,
		ReturnConvProb:   0.89,
		MaxMarketPrice:   0.0,
		MinMarketPrice:   0.05,
		MinEdgeCents:     1,
		Label:            "setpoint",
	}
}

// SetPointStrategy is a configurable set-point detection strategy.
// Generalizes MatchPointStrategy to fire on any set point, not just
// match-deciding ones. Data shows set points have 91% conversion
// but market prices them at 56c avg — a 33c edge.
type SetPointStrategy struct {
	mu            sync.RWMutex
	prices        map[string]float64
	priceTimes    map[string]time.Time
	markets       map[string][]string
	matchStates   map[string]*matchState
	seenPoints    map[string]map[string]bool
	emitter       OrderEmitter
	log           *slog.Logger
	cfg           SetPointConfig
	bankroll      float64
	kellyFraction float64
	replayNow     *time.Time
}

func NewSetPointStrategy(emitter OrderEmitter, log *slog.Logger, cfg SetPointConfig, bankroll, kellyFraction float64) *SetPointStrategy {
	return &SetPointStrategy{
		prices:        make(map[string]float64),
		priceTimes:    make(map[string]time.Time),
		markets:       make(map[string][]string),
		matchStates:   make(map[string]*matchState),
		seenPoints:    make(map[string]map[string]bool),
		emitter:       emitter,
		log:           log,
		cfg:           cfg,
		bankroll:      bankroll,
		kellyFraction: kellyFraction,
	}
}

func (s *SetPointStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *SetPointStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.matchStates, eventTicker)
	delete(s.seenPoints, eventTicker)
	s.mu.Unlock()
}

func (s *SetPointStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *SetPointStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *SetPointStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SetPointStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SetPointStrategy) OnPoints(pts []store.Point) {
	for _, p := range pts {
		s.updateMatchState(p)
		s.processPoint(p)
	}
}

func (s *SetPointStrategy) updateMatchState(p store.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.matchStates[p.MatchTicker]
	if !ok {
		ms = &matchState{}
		s.matchStates[p.MatchTicker] = ms
	}
	if p.SetNumber > ms.lastSetNum && ms.lastSetNum > 0 {
		if ms.lastHomeGames > ms.lastAwayGames {
			ms.setsHome++
		} else if ms.lastAwayGames > ms.lastHomeGames {
			ms.setsAway++
		} else if ms.lastScorer != 0 {
			if ms.lastScorer == 1 {
				ms.setsHome++
			} else {
				ms.setsAway++
			}
		}
	}
	ms.lastSetNum = p.SetNumber
	ms.lastHomeGames = p.HomeGames
	ms.lastAwayGames = p.AwayGames
	ms.lastScorer = p.Scorer
}

func (s *SetPointStrategy) processPoint(p store.Point) {
	pointKey := fmt.Sprintf("%d:%d:%d", p.SetNumber, p.GameNumber, p.PointNumber)
	s.mu.Lock()
	if s.seenPoints[p.MatchTicker] == nil {
		s.seenPoints[p.MatchTicker] = make(map[string]bool)
	}
	if s.seenPoints[p.MatchTicker][pointKey] {
		s.mu.Unlock()
		return
	}
	s.seenPoints[p.MatchTicker][pointKey] = true
	s.mu.Unlock()

	sp := s.detectSetPoint(p)
	if sp == nil {
		return
	}

	isServing := (sp.winner == 1 && p.Server == 1) || (sp.winner == 2 && p.Server == 2)
	if !isServing && !s.cfg.IncludeReturning {
		return
	}

	s.mu.RLock()
	mktTickers, ok := s.markets[p.MatchTicker]
	s.mu.RUnlock()
	if !ok || len(mktTickers) < 2 {
		return
	}

	var marketTicker string
	if sp.winner == 1 {
		marketTicker = mktTickers[0]
	} else {
		marketTicker = mktTickers[1]
	}

	convProb := s.cfg.ServeConvProb
	if !isServing {
		convProb = s.cfg.ReturnConvProb
	}

	s.mu.RLock()
	mktPrice := s.prices[marketTicker]
	priceTime := s.priceTimes[marketTicker]
	s.mu.RUnlock()
	if mktPrice <= 0 {
		return
	}
	if mktPrice < s.cfg.MinMarketPrice {
		return
	}
	if s.cfg.MaxMarketPrice > 0 && mktPrice > s.cfg.MaxMarketPrice {
		return
	}
	age := s.now().Sub(priceTime)
	if age > priceStaleTTL {
		return
	}

	edgeCents := int((convProb-mktPrice)*100 + 1e-9)
	if edgeCents < s.cfg.MinEdgeCents {
		return
	}

	size := kellySize(convProb, mktPrice, s.bankroll, s.kellyFraction)
	if size <= 0 {
		size = 10.0
	}

	payload, _ := json.Marshal(map[string]any{
		"home_games": p.HomeGames, "away_games": p.AwayGames,
		"home_points": p.HomePoints, "away_points": p.AwayPoints,
		"server": p.Server, "scorer": p.Scorer,
		"set": p.SetNumber, "game": p.GameNumber,
		"serving":   isServing,
		"is_mp":     sp.isMatchPoint,
		"set_score": fmt.Sprintf("%d-%d", s.setsHome(p.MatchTicker), s.setsAway(p.MatchTicker)),
	})

	o := store.Order{
		TS:            time.Now().UnixMilli(),
		MatchTicker:   p.MatchTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       sp.context,
		ConvProb:      convProb,
		MarketPrice:   mktPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
		Bankroll:      s.bankroll,
		KellyFraction: s.kellyFraction,
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("setpoint: order dropped", "match", p.MatchTicker, "market", marketTicker)
		return
	}
	s.log.Info("setpoint: order emitted",
		"match", p.MatchTicker, "market", marketTicker,
		"action", "buy", "edge_cents", edgeCents, "conv_prob", convProb,
		"mkt_price", mktPrice, "size", size, "context", sp.context,
		"serving", isServing, "is_mp", sp.isMatchPoint)
}

func (s *SetPointStrategy) setsHome(matchTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms, ok := s.matchStates[matchTicker]; ok {
		return ms.setsHome
	}
	return 0
}

func (s *SetPointStrategy) setsAway(matchTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms, ok := s.matchStates[matchTicker]; ok {
		return ms.setsAway
	}
	return 0
}

type setPointSignal struct {
	winner       int
	context      string
	isMatchPoint bool
}

func (s *SetPointStrategy) detectSetPoint(p store.Point) *setPointSignal {
	s.mu.RLock()
	ms := s.matchStates[p.MatchTicker]
	var setsHome, setsAway int
	if ms != nil {
		setsHome = ms.setsHome
		setsAway = ms.setsAway
	}
	s.mu.RUnlock()

	if p.IsTiebreak {
		return nil
	}

	homeNeedsSet := setsToWin - setsHome
	awayNeedsSet := setsToWin - setsAway
	if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
		return nil
	}

	homeOneSetAway := homeNeedsSet == 1
	awayOneSetAway := awayNeedsSet == 1

	homeCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 1)
	awayCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 2)

	homeCanWinSet := homeCanWinGame && p.HomeGames >= gamesPerSet-1 && p.HomeGames > p.AwayGames
	awayCanWinSet := awayCanWinGame && p.AwayGames >= gamesPerSet-1 && p.AwayGames > p.HomeGames

	if !homeCanWinSet && !awayCanWinSet {
		return nil
	}

	homeIsMP := homeCanWinSet && homeOneSetAway
	awayIsMP := awayCanWinSet && awayOneSetAway

	// If IncludeSetPoints is false, only fire on match points
	if !s.cfg.IncludeSetPoints && !homeIsMP && !awayIsMP {
		return nil
	}

	winner := 2
	ctx := "away_set_point"
	if homeCanWinSet {
		winner = 1
		ctx = "home_set_point"
	}
	if homeIsMP {
		ctx = "home_match_point"
	} else if awayIsMP {
		ctx = "away_match_point"
	}

	return &setPointSignal{
		winner:       winner,
		context:      ctx,
		isMatchPoint: homeIsMP || awayIsMP,
	}
}

func (s *SetPointStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *SetPointStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *SetPointStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *SetPointStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SetPointStrategy{%s: markets=%d, prices=%d, states=%d}",
		s.cfg.Label, len(s.markets), len(s.prices), len(s.matchStates))
}
