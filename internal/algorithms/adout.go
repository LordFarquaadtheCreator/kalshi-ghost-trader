package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// AdOutStrategy exploits the apitennis latency edge on ad-out points.
//
// Ad-out = returner has Advantage (can win game with next point = break).
// Research (RQ7) shows returner wins the point 82% of the time from ad-out.
// Research (RQ6) shows apitennis delivers scores ~835ms before kalshi prices
// react. Combined: buy returner YES before kalshi moves, sell after the
// point is played when price has caught up.
//
// Lifecycle per ad-out event:
//   1. OnPoint detects ad-out → buy returner YES at current market price
//   2. Next OnPoint for same match → sell returner YES at current market price
//      - If returner won (break): price jumped up → profit
//      - If server won (deuce): price dropped slightly → small loss
//
// One open position per match at a time. No stacking. Tiebreaks skipped
// (ad logic in tiebreaks is different — no deuce/advantage cycle).
type AdOutStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64   // market_ticker → latest YES price
	priceTimes map[string]time.Time // market_ticker → last price update
	markets    map[string][]string  // event_ticker → [home, away] market tickers
	open       map[string]*adOutPos // event_ticker → open position
	emitter    OrderEmitter
	cfg        AdOutConfig
	log        *slog.Logger
	replayNow  *time.Time
}

// adOutPos tracks an open ad-out position awaiting sell on next point.
type adOutPos struct {
	MarketTicker string
	BuyPrice     float64
	BuySize      float64
	BuyPointTS   int64 // ts_ms of the point that triggered the buy
	BuySetNum    int
	BuyGameNum   int
}

// AdOutConfig configures the ad-out latency strategy.
type AdOutConfig struct {
	MinMarketPrice float64 // don't buy below this price (liquidity floor)
	MaxMarketPrice float64 // don't buy above this price (edge ceiling)
	MinEdgeCents   int     // minimum edge (conv_prob - price) * 100 to fire
	Label          string
}

// DefaultAdOutConfig returns sensible defaults.
// ConvProb=0.82 from RQ7 (returner wins ad-out point 82% of the time).
// MaxMarketPrice=0.85 — if returner YES is already >85c, edge is too thin.
// MinEdgeCents=3 — need at least 3c edge over the 82% fair value.
func DefaultAdOutConfig() AdOutConfig {
	return AdOutConfig{
		MinMarketPrice: 0.05,
		MaxMarketPrice: 0.85,
		MinEdgeCents:   3,
		Label:          "adout",
	}
}

// adOutConvProb is the empirical returner win rate from ad-out.
// From RQ7: 82% of ad-out points are won by the returner.
const adOutConvProb = 0.82

// NewAdOutStrategy creates an ad-out latency-edge strategy.
func NewAdOutStrategy(emitter OrderEmitter, log *slog.Logger, cfg AdOutConfig) *AdOutStrategy {
	return &AdOutStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		open:       make(map[string]*adOutPos),
		emitter:    emitter,
		cfg:        cfg,
		log:        log,
	}
}

func (s *AdOutStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *AdOutStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *AdOutStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *AdOutStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *AdOutStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *AdOutStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.open, eventTicker)
	s.mu.Unlock()
}

func (s *AdOutStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *AdOutStrategy) OnPoint(eventTicker string, p store.Point) {
	// First: if we have an open position for this match, sell it.
	// This catches both break (next point in new game) and deuce (next
	// point in same game) cases.
	s.maybeSell(eventTicker, p)

	// Then: check if this point is an ad-out, and buy if so.
	s.maybeBuy(eventTicker, p)
}

// maybeSell sells any open ad-out position for this match on the next
// point event. The price will have moved by now (kalshi reacted to the
// point outcome). Clean exit regardless of break or deuce result.
func (s *AdOutStrategy) maybeSell(eventTicker string, p store.Point) {
	s.mu.Lock()
	pos := s.open[eventTicker]
	if pos == nil {
		s.mu.Unlock()
		return
	}
	// Don't sell on the same point that triggered the buy.
	if p.TS == pos.BuyPointTS && p.SetNumber == pos.BuySetNum && p.GameNumber == pos.BuyGameNum {
		s.mu.Unlock()
		return
	}
	// Clear the open position before emitting to avoid re-entry.
	delete(s.open, eventTicker)
	s.mu.Unlock()

	// Fetch current price for the sell.
	s.mu.RLock()
	price := s.prices[pos.MarketTicker]
	priceTime := s.priceTimes[pos.MarketTicker]
	s.mu.RUnlock()

	if price <= 0 || s.now().Sub(priceTime) > priceStaleTTL {
		// No fresh price — can't sell. Position will settle at market close.
		s.log.Warn("adout: sell skipped, no fresh price",
			"event", eventTicker, "market", pos.MarketTicker,
			"buy_price", pos.BuyPrice, "buy_size", pos.BuySize)
		return
	}

	size := pos.BuySize
	if size < 1 {
		size = 1
	}

	s.emitter.EmitOrder(store.Order{
		MatchTicker:   eventTicker,
		MarketTicker:  pos.MarketTicker,
		Action:        "sell",
		Side:          store.OrderSideClose,
		Context:       fmt.Sprintf("adout_sell_set%d_game%d", p.SetNumber, p.GameNumber),
		ConvProb:      adOutConvProb,
		MarketPrice:   price,
		EdgeCents:     int((price - pos.BuyPrice) * 100),
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
	})

	s.log.Debug("adout: sell signal",
		"event", eventTicker, "market", pos.MarketTicker,
		"buy_price", pos.BuyPrice, "sell_price", price,
		"size", size,
		"pnl_cents", int((price-pos.BuyPrice)*size*100))
}

// maybeBuy detects ad-out and buys returner YES.
// Ad-out: returner has Advantage ("A" in their points field).
//   server=1 (home serving) && away_points=="A" → returner is away (player 2)
//   server=2 (away serving) && home_points=="A" → returner is home (player 1)
func (s *AdOutStrategy) maybeBuy(eventTicker string, p store.Point) {
	if p.IsTiebreak {
		return // tiebreak ad logic is different — no deuce/advantage cycle
	}

	// Detect ad-out: returner has "A"
	returner := 0
	if p.Server == 1 && p.AwayPoints == "A" {
		returner = 2 // away is returner, has advantage
	} else if p.Server == 2 && p.HomePoints == "A" {
		returner = 1 // home is returner, has advantage
	} else {
		return // not ad-out
	}

	// Already have an open position for this match — don't stack.
	s.mu.RLock()
	hasOpen := s.open[eventTicker] != nil
	s.mu.RUnlock()
	if hasOpen {
		return
	}

	// Look up returner's market ticker.
	s.mu.RLock()
	mkts, ok := s.markets[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mkts) < 2 {
		return
	}
	returnerMkt := mkts[returner-1] // mkts[0]=home, mkts[1]=away

	// Fetch current price.
	s.mu.RLock()
	price := s.prices[returnerMkt]
	priceTime := s.priceTimes[returnerMkt]
	s.mu.RUnlock()

	if price <= 0 || s.now().Sub(priceTime) > priceStaleTTL {
		s.log.Debug("adout: no fresh price, skipping buy",
			"event", eventTicker, "market", returnerMkt)
		return
	}

	// Edge = (conv_prob - market_price) * 100 cents.
	// Conv prob = 82% (returner wins ad-out point).
	edgeCents := int((adOutConvProb - price) * 100)
	if edgeCents < s.cfg.MinEdgeCents {
		s.log.Debug("adout: edge too small",
			"event", eventTicker, "conv_prob", adOutConvProb,
			"price", price, "edge", edgeCents)
		return
	}
	if price < s.cfg.MinMarketPrice {
		return
	}
	if s.cfg.MaxMarketPrice > 0 && price > s.cfg.MaxMarketPrice {
		return
	}

	size := kellySized(adOutConvProb, price)

	// Record open position before emitting.
	s.mu.Lock()
	s.open[eventTicker] = &adOutPos{
		MarketTicker: returnerMkt,
		BuyPrice:     price,
		BuySize:      size,
		BuyPointTS:   p.TS,
		BuySetNum:    p.SetNumber,
		BuyGameNum:   p.GameNumber,
	}
	s.mu.Unlock()

	s.emitter.EmitOrder(store.Order{
		MatchTicker:   eventTicker,
		MarketTicker:  returnerMkt,
		Action:        "buy",
		Side:          store.OrderSideOpen,
		Context:       fmt.Sprintf("adout_buy_set%d_game%d_pt%d", p.SetNumber, p.GameNumber, p.PointNumber),
		ConvProb:      adOutConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
	})

	s.log.Debug("adout: buy signal",
		"event", eventTicker, "market", returnerMkt,
		"price", price, "edge", edgeCents, "size", size,
		"server", p.Server, "returner", returner,
		"home_points", p.HomePoints, "away_points", p.AwayPoints)
}

func (s *AdOutStrategy) OnTick(ctx context.Context) {}

func (s *AdOutStrategy) String() string {
	return fmt.Sprintf("AdOutStrategy{markets=%d, open=%d}", len(s.markets), len(s.open))
}
