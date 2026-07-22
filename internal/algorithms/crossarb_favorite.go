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

// CrossArbFavoriteConfig controls the directional favorite-fade variant
// of cross-arb. When yesSum > 1.0 + threshold AND one NO side is cheap
// (favorite overpriced), buy NO of the favorite only. Skip the expensive
// NO hedge — empirical data shows it loses 86% of the time.
//
// Backtest (9 days, 1675 cross-arb orders): buy_no with NO<30c hit 75.9%,
// ROI 373%, vs buy_no with NO>=50c hit 13.6%, ROI -57%. Dropping the
// expensive hedge lifts ROI from 60% to 373% on the same signal.
type CrossArbFavoriteConfig struct {
	// MinEdgeCents: minimum yesSum-1.0 edge in cents to trigger (default 2).
	MinEdgeCents int
	// MaxNOPrice: only fire when the favorite's NO price is at or below this
	// (default 0.30 = favorite YES >= 0.70).
	MaxNOPrice float64
	// Label: strategy label.
	Label string
}

func DefaultCrossArbFavoriteConfig() CrossArbFavoriteConfig {
	return CrossArbFavoriteConfig{
		MinEdgeCents: 2,
		MaxNOPrice:   0.30,
		Label:        "cross-arb-favorite",
	}
}

// CrossArbFavoriteStrategy monitors both YES markets for the same event.
// When yesSum > 1.0 + threshold, identifies the favorite (higher YES price,
// cheaper NO) and buys NO of that side only — directional fade of the
// overpriced favorite. Skips the underdog NO hedge and the buy_both_yes
// path entirely (both net-negative in backtest).
type CrossArbFavoriteStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       CrossArbFavoriteConfig
	replayNow *time.Time
}

func NewCrossArbFavoriteStrategy(emitter OrderEmitter, log *slog.Logger, cfg CrossArbFavoriteConfig) *CrossArbFavoriteStrategy {
	return &CrossArbFavoriteStrategy{
		prices:  make(map[string]float64),
		markets: make(map[string][]string),
		fired:   make(map[string]bool),
		emitter: emitter,
		log:     log,
		cfg:     cfg,
	}
}

func (s *CrossArbFavoriteStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *CrossArbFavoriteStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *CrossArbFavoriteStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *CrossArbFavoriteStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
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
	noEdgeCents := int((yesSum - 1.0) * 100)
	if noEdgeCents < s.cfg.MinEdgeCents {
		s.mu.Unlock()
		return
	}

	// Favorite = higher YES price = cheaper NO. Fade the favorite's NO only.
	favMkt, favYes := homeMkt, homePrice
	if awayPrice > homePrice {
		favMkt, favYes = awayMkt, awayPrice
	}
	noPrice := 1.0 - favYes
	if noPrice > s.cfg.MaxNOPrice {
		s.mu.Unlock()
		return
	}

	s.fired[eventTicker] = true
	s.mu.Unlock()

	s.fireBuyFavoriteNO(eventTicker, favMkt, favYes, noPrice, noEdgeCents, ts)
}

func (s *CrossArbFavoriteStrategy) fireBuyFavoriteNO(eventTicker, favMkt string, favYes, noPrice float64, edgeCents int, ts time.Time) {
	payload, _ := json.Marshal(map[string]any{
		"favorite_yes": favYes,
		"no_price":     noPrice,
		"yes_sum":      favYes + (1.0 - noPrice), // approx — opposite side implied
		"edge_cents":   edgeCents,
		"side":         "buy_favorite_no",
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  favMkt,
		Action:        "buy_no",
		Context:       fmt.Sprintf("crossarbfav_buy_no_edge%dc", edgeCents),
		ConvProb:      favYes, // NO wins when YES loses
		MarketPrice:   noPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: kellySized(favYes, noPrice),
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}
	if !s.emitter.EmitOrder(o) {
		s.log.Warn("cross-arb-favorite: order dropped", "match", eventTicker, "market", favMkt)
		return
	}
	s.log.Info("cross-arb-favorite: buy favorite NO",
		"match", eventTicker, "market", favMkt, "fav_yes", favYes,
		"no_price", noPrice, "edge_cents", edgeCents)
}

func (s *CrossArbFavoriteStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *CrossArbFavoriteStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *CrossArbFavoriteStrategy) eventForMarket(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *CrossArbFavoriteStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *CrossArbFavoriteStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("CrossArbFavoriteStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

func (s *CrossArbFavoriteStrategy) OnTick(ctx context.Context) {}
