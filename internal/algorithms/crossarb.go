package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// CrossArbConfig controls the cross-side arbitrage strategy.
// When YES_home + YES_away < 1.0 - threshold, buy both YES (guaranteed profit).
// When YES_home + YES_away > 1.0 + threshold, buy both NO (guaranteed profit).
// Data: 4463 instances of >2c arb in 51k timestamp matches.
type CrossArbConfig struct {
	// MinEdgeCents: minimum arb edge to trigger (default 2 = 2c per pair)
	MinEdgeCents int
	// BaseSize: order size per side in dollars
	BaseSize float64
	// Label: strategy label
	Label string
}

func DefaultCrossArbConfig() CrossArbConfig {
	return CrossArbConfig{
		MinEdgeCents: 2,
		BaseSize:     10.0,
		Label:        "cross-arb",
	}
}

// CrossArbStrategy monitors both YES markets for the same event.
// When sum of YES prices < 1.0 - threshold, buys both YES.
// When sum > 1.0 + threshold, buys both NO (via YES of opposite market).
// Fires once per event.
type CrossArbStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       CrossArbConfig
	replayNow *time.Time
}

func NewCrossArbStrategy(emitter OrderEmitter, log *slog.Logger, cfg CrossArbConfig) *CrossArbStrategy {
	return &CrossArbStrategy{
		prices:  make(map[string]float64),
		markets: make(map[string][]string),
		fired:   make(map[string]bool),
		emitter: emitter,
		log:     log,
		cfg:     cfg,
	}
}

func (s *CrossArbStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *CrossArbStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *CrossArbStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *CrossArbStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	eventTicker := s.eventForMarket(marketTicker)
	if eventTicker == "" || s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	mkts, ok := s.markets[eventTicker]
	if !ok || len(mkts) < 2 {
		s.mu.Unlock()
		return
	}

	homeMkt, awayMkt := mkts[0], mkts[1]
	homePrice := s.prices[homeMkt]
	awayPrice := s.prices[awayMkt]

	if homePrice <= 0 || awayPrice <= 0 {
		s.mu.Unlock()
		return
	}

	yesSum := homePrice + awayPrice
	edgeCents := int((1.0 - yesSum) * 100)

	if edgeCents < s.cfg.MinEdgeCents {
		// Check NO arb: if yesSum > 1.0 + threshold, buy both NO
		noEdgeCents := int((yesSum - 1.0) * 100)
		if noEdgeCents < s.cfg.MinEdgeCents {
			s.mu.Unlock()
			return
		}
		// Buy NO on both sides = sell YES equivalent
		// On Kalshi, buying NO at price p is same as selling YES at 1-p
		// NO_home price ≈ 1 - homePrice, NO_away ≈ 1 - awayPrice
		// no_sum = (1-homePrice) + (1-awayPrice) = 2 - yesSum
		// If yesSum > 1.0, no_sum < 1.0 → arb exists on NO side
		s.fired[eventTicker] = true
		s.mu.Unlock()

		s.fireBuyNO(eventTicker, homeMkt, awayMkt, homePrice, awayPrice, noEdgeCents, ts)
		return
	}

	s.fired[eventTicker] = true
	s.mu.Unlock()

	s.fireBuyBothYES(eventTicker, homeMkt, awayMkt, homePrice, awayPrice, edgeCents, ts)
}

func (s *CrossArbStrategy) fireBuyBothYES(eventTicker, homeMkt, awayMkt string, homePrice, awayPrice float64, edgeCents int, ts time.Time) {
	payload, _ := json.Marshal(map[string]any{
		"home_yes": homePrice, "away_yes": awayPrice,
		"yes_sum":  homePrice + awayPrice,
		"edge_cents": edgeCents,
		"side": "buy_both_yes",
	})

	for _, mkt := range []string{homeMkt, awayMkt} {
		price := homePrice
		if mkt == awayMkt {
			price = awayPrice
		}
		o := store.Order{
			TS:            ts.UnixMilli(),
			MatchTicker:   eventTicker,
			MarketTicker:  mkt,
			Action:        "buy",
			Context:       fmt.Sprintf("crossarb_buy_yes_edge%dc", edgeCents),
			ConvProb:      1.0 - price, // approx — arb doesn't need conv prob
			MarketPrice:   price,
			EdgeCents:     edgeCents,
			SuggestedSize: s.cfg.BaseSize,
			Strategy:      s.cfg.Label,
			Payload:       string(payload),
		}
		if !s.emitter.EmitOrder(o) {
			s.log.Warn("cross-arb: order dropped", "match", eventTicker, "market", mkt)
			return
		}
	}
	s.log.Info("cross-arb: buy both YES",
		"match", eventTicker, "home", homePrice, "away", awayPrice,
		"sum", homePrice+awayPrice, "edge_cents", edgeCents)
}

func (s *CrossArbStrategy) fireBuyNO(eventTicker, homeMkt, awayMkt string, homePrice, awayPrice float64, edgeCents int, ts time.Time) {
	payload, _ := json.Marshal(map[string]any{
		"home_yes": homePrice, "away_yes": awayPrice,
		"yes_sum":  homePrice + awayPrice,
		"edge_cents": edgeCents,
		"side": "buy_both_no",
	})

	// Buy NO = buy YES of the opposite player's market
	// On Kalshi, each market is a YES market for one player.
	// To bet NO on home, buy YES on away. To bet NO on away, buy YES on home.
	// But we already have both YES markets. If yesSum > 1.0,
	// it means both YES are overpriced. We can't directly sell.
	// On Kalshi, we CAN buy NO shares in each market.
	// NO price ≈ 1 - YES price. Buying NO at (1-homePrice) + (1-awayPrice) = 2 - yesSum < 1.0
	// But our emitter only supports buy YES orders.
	// For paper trading, we record the arb signal. Real execution would buy NO.
	for _, mkt := range []string{homeMkt, awayMkt} {
		price := homePrice
		if mkt == awayMkt {
			price = awayPrice
		}
		noPrice := 1.0 - price
		o := store.Order{
			TS:            ts.UnixMilli(),
			MatchTicker:   eventTicker,
			MarketTicker:  mkt,
			Action:        "buy_no",
			Context:       fmt.Sprintf("crossarb_buy_no_edge%dc", edgeCents),
			ConvProb:      price, // NO wins when YES loses
			MarketPrice:   noPrice,
			EdgeCents:     edgeCents,
			SuggestedSize: s.cfg.BaseSize,
			Strategy:      s.cfg.Label,
			Payload:       string(payload),
		}
		if !s.emitter.EmitOrder(o) {
			s.log.Warn("cross-arb: NO order dropped", "match", eventTicker, "market", mkt)
			return
		}
	}
	s.log.Info("cross-arb: buy both NO",
		"match", eventTicker, "home", homePrice, "away", awayPrice,
		"sum", homePrice+awayPrice, "edge_cents", edgeCents)
}

func (s *CrossArbStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *CrossArbStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *CrossArbStrategy) eventForMarket(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *CrossArbStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *CrossArbStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("CrossArbStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}
