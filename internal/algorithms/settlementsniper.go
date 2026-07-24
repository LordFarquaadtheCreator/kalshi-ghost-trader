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

// SettlementSniperStrategy buys YES on the guaranteed winner after the
// match ends but before the market settles. Kalshi keeps markets open
// for minutes after match completion — during that window the winner's
// YES trades below $1.00. ConvProb is 1.0 (result already known), so
// any price < 1.0 is positive edge.
//
// Live mode: OnMatchFinished dispatched by API-Tennis scraper when
// EventStatus == "Finished". Sub-second detection latency.
//
// Backtest mode: infers match completion from OnPoint by tracking sets
// won. When a player reaches setsToWin, fires. No stored status events
// exist in the points table.
type SettlementSniperStrategy struct {
	mu sync.RWMutex

	prices     map[string]float64     // market_ticker -> latest YES price
	priceTimes map[string]time.Time   // market_ticker -> last price update
	markets    map[string][]string    // event_ticker -> [home, away]
	fired      map[string]bool        // event_ticker -> already fired
	matchState map[string]*sniperState // event_ticker -> set tracking

	emitter OrderEmitter
	log     *slog.Logger

	replayNow *time.Time
}

// sniperState tracks sets won for backtest match-completion inference.
type sniperState struct {
	setsHome      int
	setsAway      int
	lastSetNum    int
	lastHomeGames int
	lastAwayGames int
	lastScorer    int
}

// NewSettlementSniperStrategy creates a settlement sniper strategy.
func NewSettlementSniperStrategy(emitter OrderEmitter, log *slog.Logger) *SettlementSniperStrategy {
	return &SettlementSniperStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		fired:      make(map[string]bool),
		matchState: make(map[string]*sniperState),
		emitter:    emitter,
		log:        log,
	}
}

func (s *SettlementSniperStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *SettlementSniperStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.matchState, eventTicker)
	s.mu.Unlock()
}

func (s *SettlementSniperStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *SettlementSniperStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *SettlementSniperStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SettlementSniperStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SettlementSniperStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *SettlementSniperStrategy) OnTick(ctx context.Context) {}

// PreMatchGated prevents orders before the match starts.
func (s *SettlementSniperStrategy) PreMatchGated() {}

// OnPoint tracks match state for backtest match-completion inference.
// When a player reaches setsToWin sets, dispatches the buy.
func (s *SettlementSniperStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)

	s.mu.RLock()
	ms := s.matchState[eventTicker]
	s.mu.RUnlock()
	if ms == nil {
		return
	}

	if ms.setsHome >= setsToWin {
		s.fire(eventTicker, 1)
	} else if ms.setsAway >= setsToWin {
		s.fire(eventTicker, 2)
	}
}

func (s *SettlementSniperStrategy) updateMatchState(eventTicker string, p store.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.matchState[eventTicker]
	if !ok {
		ms = &sniperState{}
		s.matchState[eventTicker] = ms
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

// OnMatchFinished is the live-mode entry point. Dispatched by the
// API-Tennis scraper when EventStatus == "Finished".
func (s *SettlementSniperStrategy) OnMatchFinished(eventTicker string, winner int) {
	s.fire(eventTicker, winner)
}

// fire emits a buy order on the winner's market if price < 1.0 and
// the event hasn't already been fired.
func (s *SettlementSniperStrategy) fire(eventTicker string, winner int) {
	s.mu.Lock()
	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}
	s.fired[eventTicker] = true
	mktTickers, ok := s.markets[eventTicker]
	s.mu.Unlock()

	if !ok || len(mktTickers) < 2 {
		return
	}

	var marketTicker string
	if winner == 1 {
		marketTicker = mktTickers[0]
	} else {
		marketTicker = mktTickers[1]
	}

	s.mu.RLock()
	mktPrice := s.prices[marketTicker]
	priceTime := s.priceTimes[marketTicker]
	s.mu.RUnlock()

	if mktPrice <= 0 || mktPrice >= 1.0 {
		s.log.Debug("settlementsniper: no edge or no price",
			"event", eventTicker, "market", marketTicker,
			"price", mktPrice, "winner", winner)
		return
	}

	age := s.now().Sub(priceTime)
	if age > priceStaleTTL {
		s.log.Debug("settlementsniper: price stale",
			"event", eventTicker, "market", marketTicker,
			"age_s", age.Seconds())
		return
	}

	// Result is known — ConvProb = 1.0. Edge = (1.0 - price) * 100 cents.
	convProb := 1.0
	edgeCents := int((convProb-mktPrice)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	size := kellySized(convProb, mktPrice)
	if size <= 0 {
		size = 5.0 / mktPrice
	}

	payload, _ := json.Marshal(map[string]any{
		"winner":    winner,
		"conv_prob": convProb,
		"trigger":   "match_finished",
	})

	o := store.Order{
		TS:            s.now().UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       "settlement_sniper",
		ConvProb:      convProb,
		MarketPrice:   mktPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      "settlementsniper",
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("settlementsniper: order dropped",
			"match", eventTicker, "market", marketTicker)
		return
	}
	s.log.Info("settlementsniper: order emitted",
		"match", eventTicker, "market", marketTicker,
		"price", mktPrice, "edge_cents", edgeCents, "size", size,
		"winner", winner)
}

func (s *SettlementSniperStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SettlementSniperStrategy{markets=%d, fired=%d}",
		len(s.markets), len(s.fired))
}
