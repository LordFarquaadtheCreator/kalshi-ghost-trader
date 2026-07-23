package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// ConvexPoolAdaptiveStrategy is ConvexPoolStrategy with a dynamic alpha
// that scales with score depth. Early in the match (set 1, few games
// played), alpha is low — trust the market, which incorporates pre-match
// odds and early information. As the match progresses deeper into sets,
// alpha rises — the Markov model becomes more informative because it
// captures the exact score state, while the market may lag.
//
// Score depth proxy (best-of-3):
//   progress = (setsHome + setsAway) / 2.0          // 0, 0.5, 1.0
//            + (gamesHome + gamesAway) / 12.0 * 0.3 // small bonus within set
//   alpha = AlphaMin + (AlphaMax - AlphaMin) * clamp(progress, 0, 1)
//
// At set 1 start: progress ≈ 0 → alpha = AlphaMin (trust market).
// At deciding set 3 deep: progress ≈ 1 → alpha = AlphaMax (trust model).
//
// Fires on every point update, same as ConvexPoolStrategy. Buy-only,
// no exit logic — pair with convexpool-exit for exits.
type ConvexPoolAdaptiveStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64
	priceTimes map[string]time.Time
	markets    map[string][]string
	states     map[string]*cpaMatchState
	series     map[string]string
	emitter    OrderEmitter
	db         *store.DB
	model      *MarkovModel
	cfg        ConvexPoolAdaptiveConfig
	log        *slog.Logger
	replayNow  *time.Time
}

// cpaMatchState tracks sets won + games in current set for alpha scaling.
type cpaMatchState struct {
	setsHome  int
	setsAway  int
	gamesHome int
	gamesAway int
}

// ConvexPoolAdaptiveConfig configures the adaptive-alpha convex pool.
type ConvexPoolAdaptiveConfig struct {
	PServe         float64
	AlphaMin       float64 // alpha at match start (low = trust market)
	AlphaMax       float64 // alpha deep in match (high = trust model)
	MinEdgeCents   int
	MinMarketPrice float64
	MaxMarketPrice float64
	Label          string
	SeriesFilter   []string
	UTCHourStart   int
	UTCHourEnd     int
}

// DefaultConvexPoolAdaptiveConfig returns sensible defaults.
func DefaultConvexPoolAdaptiveConfig() ConvexPoolAdaptiveConfig {
	return ConvexPoolAdaptiveConfig{
		PServe:         0.64,
		AlphaMin:       0.3, // early match: 70% market, 30% model
		AlphaMax:       0.8, // deep match: 20% market, 80% model
		MinEdgeCents:   3,
		MinMarketPrice: 0.05,
		MaxMarketPrice: 0.95,
		Label:          "convexpool-adaptive",
	}
}

// NewConvexPoolAdaptiveStrategy creates an adaptive-alpha convex pool.
func NewConvexPoolAdaptiveStrategy(emitter OrderEmitter, log *slog.Logger, cfg ConvexPoolAdaptiveConfig) *ConvexPoolAdaptiveStrategy {
	return &ConvexPoolAdaptiveStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		states:     make(map[string]*cpaMatchState),
		series:     make(map[string]string),
		emitter:    emitter,
		model:      NewMarkovModelWithProb(cfg.PServe),
		cfg:        cfg,
		log:        log,
	}
}

// NewConvexPoolAdaptiveStrategyWithDB creates a live-mode adaptive strat.
func NewConvexPoolAdaptiveStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg ConvexPoolAdaptiveConfig) *ConvexPoolAdaptiveStrategy {
	s := NewConvexPoolAdaptiveStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *ConvexPoolAdaptiveStrategy) SetSharedMarkovModel(m *MarkovModel) {
	s.model = m
}

func (s *ConvexPoolAdaptiveStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

func (s *ConvexPoolAdaptiveStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

func (s *ConvexPoolAdaptiveStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

func (s *ConvexPoolAdaptiveStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *ConvexPoolAdaptiveStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *ConvexPoolAdaptiveStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	if _, ok := s.states[eventTicker]; !ok {
		s.states[eventTicker] = &cpaMatchState{}
	}
	s.mu.Unlock()

	if s.db != nil {
		s.loadSeriesTicker(eventTicker)
	}
}

func (s *ConvexPoolAdaptiveStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
}

func (s *ConvexPoolAdaptiveStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.states, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *ConvexPoolAdaptiveStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *ConvexPoolAdaptiveStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)
	s.processAdaptive(eventTicker, p)
}

func (s *ConvexPoolAdaptiveStrategy) updateMatchState(eventTicker string, p store.Point) {
	s.mu.Lock()
	ms := s.states[eventTicker]
	if ms == nil {
		ms = &cpaMatchState{}
		s.states[eventTicker] = ms
	}
	if p.HomeSetGames > ms.setsHome {
		ms.setsHome = p.HomeSetGames
	}
	if p.AwaySetGames > ms.setsAway {
		ms.setsAway = p.AwaySetGames
	}
	ms.gamesHome = p.HomeGames
	ms.gamesAway = p.AwayGames
	s.mu.Unlock()
}

// computeAlpha calculates dynamic alpha based on score depth.
// Early match → low alpha (trust market). Deep match → high alpha (trust model).
func (s *ConvexPoolAdaptiveStrategy) computeAlpha(setsHome, setsAway, gamesHome, gamesAway int) float64 {
	// Sets progress: 0 (set 1 start) → 1.0 (deciding set 3).
	setsProgress := float64(setsHome+setsAway) / 2.0
	if setsProgress > 1.0 {
		setsProgress = 1.0
	}

	// Games progress within current set: 0-12 games, small bonus.
	gamesProgress := float64(gamesHome+gamesAway) / 12.0 * 0.3
	if gamesProgress > 0.3 {
		gamesProgress = 0.3
	}

	progress := setsProgress + gamesProgress
	if progress > 1.0 {
		progress = 1.0
	}
	if progress < 0 {
		progress = 0
	}

	return s.cfg.AlphaMin + (s.cfg.AlphaMax-s.cfg.AlphaMin)*progress
}

func (s *ConvexPoolAdaptiveStrategy) processAdaptive(eventTicker string, p store.Point) {
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

	s.mu.RLock()
	ms := s.states[eventTicker]
	s.mu.RUnlock()
	if ms == nil {
		return
	}

	alpha := s.computeAlpha(ms.setsHome, ms.setsAway, ms.gamesHome, ms.gamesAway)

	fvHome := s.model.FairValue(
		ms.setsHome, ms.setsAway,
		ms.gamesHome, ms.gamesAway,
		p.HomePoints, p.AwayPoints,
		p.Server, p.IsTiebreak,
	)
	fvAway := 1.0 - fvHome

	for i, mkt := range mkts {
		fv := fvHome
		if i == 1 {
			fv = fvAway
		}

		s.mu.RLock()
		price := s.prices[mkt]
		priceTime := s.priceTimes[mkt]
		s.mu.RUnlock()

		if price <= 0 || s.now().Sub(priceTime) > priceStaleTTL {
			continue
		}

		blended := alpha*fv + (1-alpha)*price
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

		s.emitter.EmitOrder(store.Order{
			TS:            s.now().UnixMilli(),
			MatchTicker:   eventTicker,
			MarketTicker:  mkt,
			Action:        "buy",
			Context:       fmt.Sprintf("cpa_%.2f_%s_set%d_game%d_pt%d", alpha, sideName(i+1), p.SetNumber, p.GameNumber, p.PointNumber),
			ConvProb:      blended,
			MarketPrice:   price,
			EdgeCents:     edgeCents,
			SuggestedSize: size,
			Bankroll:      paperBankroll,
			KellyFraction: kellyFractionP,
			SetNumber:     p.SetNumber,
			Strategy:      s.cfg.Label,
		})

		s.log.Debug("convex pool adaptive signal",
			"event", eventTicker, "market", mkt,
			"fv", fv, "blended", blended, "price", price, "edge", edgeCents,
			"alpha", alpha, "sets", fmt.Sprintf("%d-%d", ms.setsHome, ms.setsAway),
			"games", fmt.Sprintf("%d-%d", ms.gamesHome, ms.gamesAway))
	}
}

func (s *ConvexPoolAdaptiveStrategy) String() string {
	return fmt.Sprintf("ConvexPoolAdaptiveStrategy{markets=%d}", len(s.markets))
}

func (s *ConvexPoolAdaptiveStrategy) OnTick(ctx context.Context) {}
