package algorithms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// SetDownConfig controls the set-down favourite recovery strategy.
// When a favourite loses set 1, market overreacts — price drops to 0.35-0.45.
// Top-50 favourites recover 58-62% on hard court, 52% clay, 50% grass.
//
// Price-based detection: favourite's price was >MinFavPrice, now in
// [MinEntryPrice, MaxEntryPrice] range — proxies a set loss.
type SetDownConfig struct {
	// MinFavPrice: price must have exceeded this at some point (was favourite).
	MinFavPrice float64
	// MinEntryPrice: current price must be >= this to enter.
	MinEntryPrice float64
	// MaxEntryPrice: current price must be <= this to enter.
	MaxEntryPrice float64
	// ConvProb: estimated recovery probability (0.55 = conservative avg).
	ConvProb float64
	// BaseSize: order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
}

func DefaultSetDownConfig() SetDownConfig {
	return SetDownConfig{
		MinFavPrice:   0.55,
		MinEntryPrice: 0.30,
		MaxEntryPrice: 0.45,
		ConvProb:      0.55,
		BaseSize:      10.0,
		Label:         "setdown",
	}
}

// SetDownStrategy buys a favourite's YES after their price drops to a
// depressed range, betting on set recovery. Markets overreact to set losses.
type SetDownStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64  // market_ticker -> latest price
	maxPrices map[string]float64  // market_ticker -> peak price seen
	markets   map[string][]string // event_ticker -> [home, away]
	fired     map[string]bool     // event_ticker -> fired
	emitter   OrderEmitter
	log       *slog.Logger
	cfg       SetDownConfig
	replayNow *time.Time
}

func NewSetDownStrategy(emitter OrderEmitter, log *slog.Logger, cfg SetDownConfig) *SetDownStrategy {
	return &SetDownStrategy{
		prices:    make(map[string]float64),
		maxPrices: make(map[string]float64),
		markets:   make(map[string][]string),
		fired:     make(map[string]bool),
		emitter:   emitter,
		log:       log,
		cfg:       cfg,
	}
}

func (s *SetDownStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
}

func (s *SetDownStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.maxPrices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *SetDownStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *SetDownStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	if price > s.maxPrices[marketTicker] {
		s.maxPrices[marketTicker] = price
	}

	eventTicker := s.eventForMarket(marketTicker)
	if eventTicker == "" || s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	maxPrice := s.maxPrices[marketTicker]
	if maxPrice < s.cfg.MinFavPrice {
		s.mu.Unlock()
		return
	}

	// Price must have dropped from favourite territory into entry range
	if price < s.cfg.MinEntryPrice || price > s.cfg.MaxEntryPrice {
		s.mu.Unlock()
		return
	}

	// Must be a meaningful drop from peak (not just noise)
	dropFromPeak := maxPrice - price
	if dropFromPeak < 0.10 {
		s.mu.Unlock()
		return
	}

	s.fired[eventTicker] = true
	s.mu.Unlock()

	edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"max_price":      maxPrice,
		"entry_price":    price,
		"drop_from_peak": dropFromPeak,
		"conv_prob":      s.cfg.ConvProb,
		"entry_ts":       ts.UnixMilli(),
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       fmt.Sprintf("setdown_peak_%.2f_now_%.2f", maxPrice, price),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: s.cfg.BaseSize,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("setdown: order dropped", "match", eventTicker, "market", marketTicker)
		return
	}
	s.log.Info("setdown: order emitted",
		"match", eventTicker, "market", marketTicker,
		"price", price, "max_price", maxPrice,
		"edge_cents", edgeCents)
}

// OnPoint fires when a set completes and the favourite (higher peak price)
// lost it. Buys the favourite's market at the depressed price after set loss.
func (s *SetDownStrategy) OnPoint(eventTicker string, p store.Point) {
	// Detect end of set: game_number wraps to 1 and set_number increments.
	// We trigger when a set ends — check if scorer won the final game of a set.
	// Heuristic: game won (point_number=1 of next game means previous set ended)
	// Better: check if home_games or away_games reached 6 (or 7 for tiebreak).
	if p.PointNumber != 1 {
		return
	}
	if p.GameNumber != 1 {
		return
	}
	// Only fire at start of set 2+ (set 1 just ended)
	if p.SetNumber < 2 {
		return
	}

	s.mu.Lock()
	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}
	mkts, ok := s.markets[eventTicker]
	if !ok {
		s.mu.Unlock()
		return
	}

	// Determine who lost the previous set from home_set_games/away_set_games.
	// home_set_games/away_set_games = final games in completed sets before this one.
	// If home_set_games < away_set_games, home lost the last set.
	homeLostSet := p.HomeSetGames < p.AwaySetGames
	awayLostSet := p.AwaySetGames < p.HomeSetGames
	if !homeLostSet && !awayLostSet {
		s.mu.Unlock()
		return
	}

	// Buy the player who lost the set if they were the favourite (high peak price).
	targetMkt := mkts[0] // home
	if awayLostSet {
		targetMkt = mkts[1] // away
	}

	maxPrice := s.maxPrices[targetMkt]
	price := s.prices[targetMkt]
	s.fired[eventTicker] = true
	s.mu.Unlock()

	if maxPrice < s.cfg.MinFavPrice {
		return
	}
	if price < s.cfg.MinEntryPrice || price > s.cfg.MaxEntryPrice {
		return
	}

	edgeCents := int((s.cfg.ConvProb-price)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	ts := s.now()
	payload, _ := json.Marshal(map[string]any{
		"set_lost":       p.SetNumber - 1,
		"home_set_games": p.HomeSetGames,
		"away_set_games": p.AwaySetGames,
		"target_mkt":     targetMkt,
		"max_price":      maxPrice,
		"entry_price":    price,
		"conv_prob":      s.cfg.ConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  targetMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("setdown_lost_set%d", p.SetNumber-1),
		ConvProb:      s.cfg.ConvProb,
		MarketPrice:   price,
		EdgeCents:     edgeCents,
		SuggestedSize: s.cfg.BaseSize,
		SetNumber:     p.SetNumber - 1,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("setdown: order dropped", "match", eventTicker, "market", targetMkt)
		return
	}
	s.log.Info("setdown: order emitted (score-based)",
		"match", eventTicker, "market", targetMkt,
		"set_lost", p.SetNumber-1,
		"price", price, "max_price", maxPrice,
		"edge_cents", edgeCents)
}

func (s *SetDownStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SetDownStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SetDownStrategy) eventForMarket(marketTicker string) string {
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				return et
			}
		}
	}
	return ""
}

func (s *SetDownStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.maxPrices, marketTicker)
	s.mu.Unlock()
}

func (s *SetDownStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *SetDownStrategy) GetPriceAge(marketTicker string) time.Duration {
	return time.Hour
}

func (s *SetDownStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SetDownStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}
