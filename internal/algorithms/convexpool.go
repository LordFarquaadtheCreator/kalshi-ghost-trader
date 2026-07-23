package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// ConvexPoolStrategy compares Markov fair value against market price.
// Edge = fairValue - marketPrice (raw, no blend). Fires buy on the side
// with the larger positive edge when it exceeds MinEdgeCents.
//
// Per-series pServe overrides fix the model's serve-win assumption per
// tour level (ITF women's hold ~52% vs default 0.64). One open signal
// per market per cooldown window — no stacking. Skips tiebreaks (Markov
// tiebreak model is crude 50/50).
//
// Fires on every point update (not just break/set/match points), making
// it a general-purpose fair-value trader.
type ConvexPoolStrategy struct {
	mu           sync.RWMutex
	prices       map[string]float64
	priceTimes   map[string]time.Time
	markets      map[string][]string
	states       map[string]*cpMatchState
	series       map[string]string // event_ticker -> series_ticker
	lastFire     map[string]time.Time
	seriesModels map[string]*MarkovModel // lazily built from cfg.SeriesPServe
	emitter      OrderEmitter
	db           *store.DB // nil in backtest mode
	model        *MarkovModel
	cfg          ConvexPoolConfig
	log          *slog.Logger
	replayNow    *time.Time
}

type cpMatchState struct {
	setsHome int
	setsAway int
}

// ConvexPoolConfig configures the convex pool strategy.
type ConvexPoolConfig struct {
	PServe         float64 // default serve point win probability
	MinEdgeCents   int     // minimum raw Markov edge to trigger
	MinMarketPrice float64
	MaxMarketPrice float64 // 0 = no cap
	Label          string
	// SeriesFilter: if non-empty, only fire on events matching one of these
	// series tickers. Empty = no filter (all series).
	SeriesFilter []string
	// UTCHourStart / UTCHourEnd: if non-zero, only fire when entry ts UTC hour
	// falls in [Start, End). Both 0 = no filter.
	UTCHourStart int
	UTCHourEnd   int
	// SeriesPServe overrides pServe per series_ticker. Missing series fall
	// back to PServe. Fixes model bias outside WTA (ITF women's hold ~52%).
	SeriesPServe map[string]float64
	// CooldownSeconds: min seconds between fires on the same market.
	// 0 = no cooldown.
	CooldownSeconds int
}

// DefaultConvexPoolConfig returns sensible defaults.
func DefaultConvexPoolConfig() ConvexPoolConfig {
	return ConvexPoolConfig{
		PServe:          0.64,
		MinEdgeCents:    6, // raw Markov edge, was 3 with α=0.5 blend (≡6c raw)
		MinMarketPrice:  0.05,
		MaxMarketPrice:  0.95,
		Label:           "convexpool",
		CooldownSeconds: 60,
		// ITF women's: research-confirmed 52% hold. Others left at default
		// — backtest will show if they need tuning.
		SeriesPServe: map[string]float64{
			"KXITFWMATCH":   0.52,
			"KXITFWDOUBLES": 0.52,
		},
	}
}

// NewConvexPoolStrategy creates a convex pool strategy.
func NewConvexPoolStrategy(emitter OrderEmitter, log *slog.Logger, cfg ConvexPoolConfig) *ConvexPoolStrategy {
	return &ConvexPoolStrategy{
		prices:       make(map[string]float64),
		priceTimes:   make(map[string]time.Time),
		markets:      make(map[string][]string),
		states:       make(map[string]*cpMatchState),
		series:       make(map[string]string),
		lastFire:     make(map[string]time.Time),
		seriesModels: make(map[string]*MarkovModel),
		emitter:      emitter,
		model:        NewMarkovModelWithProb(cfg.PServe),
		cfg:          cfg,
		log:          log,
	}
}

// SetSharedMarkovModel replaces the per-strategy model with a shared one.
// Memoization then works across strategies with identical pServe.
// Per-series overrides (cfg.SeriesPServe) still take precedence.
func (s *ConvexPoolStrategy) SetSharedMarkovModel(m *MarkovModel) {
	s.model = m
}

// modelForSeries returns the Markov model for the given series, falling back
// to the shared/default model. Lazily builds per-series models from
// cfg.SeriesPServe on first use.
func (s *ConvexPoolStrategy) modelForSeries(series string) *MarkovModel {
	if series == "" || len(s.cfg.SeriesPServe) == 0 {
		return s.model
	}
	s.mu.RLock()
	m, ok := s.seriesModels[series]
	s.mu.RUnlock()
	if ok {
		return m
	}
	pServe, hasOverride := s.cfg.SeriesPServe[series]
	if !hasOverride {
		return s.model
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok = s.seriesModels[series]; ok {
		return m
	}
	m = NewMarkovModelWithProb(pServe)
	s.seriesModels[series] = m
	return m
}

// SetSeriesTicker maps event_ticker to series_ticker for series filtering.
// Implements SeriesSetter — called by backtest engine or live wiring.
func (s *ConvexPoolStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

// NewConvexPoolStrategyWithDB creates a live-mode convexpool that auto-loads
// series_ticker from the markets table on RegisterMarkets.
func NewConvexPoolStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg ConvexPoolConfig) *ConvexPoolStrategy {
	s := NewConvexPoolStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *ConvexPoolStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
}

// OnPriceAt sets price with an explicit timestamp. Used by backtest.
func (s *ConvexPoolStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
}

// SetReplayTime sets the virtual "now" for staleness checks in backtest mode.
func (s *ConvexPoolStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *ConvexPoolStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *ConvexPoolStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
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

func (s *ConvexPoolStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
}

func (s *ConvexPoolStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
		delete(s.lastFire, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.states, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *ConvexPoolStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *ConvexPoolStrategy) OnPoint(eventTicker string, p store.Point) {
	s.updateMatchState(eventTicker, p)
	s.processConvex(eventTicker, p)
}

func (s *ConvexPoolStrategy) updateMatchState(eventTicker string, p store.Point) {
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

func (s *ConvexPoolStrategy) processConvex(eventTicker string, p store.Point) {
	s.mu.RLock()
	mkts, ok := s.markets[eventTicker]
	series := s.series[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mkts) < 2 {
		return
	}

	// Series filter
	if !seriesMatches(series, s.cfg.SeriesFilter) {
		return
	}

	// UTC hour filter
	now := s.now()
	if !utcHourMatches(now, s.cfg.UTCHourStart, s.cfg.UTCHourEnd) {
		return
	}

	// Tiebreak guard — Markov tiebreak model is crude 50/50, no edge.
	if p.IsTiebreak {
		return
	}

	setsHome := s.getSetsHome(eventTicker)
	setsAway := s.getSetsAway(eventTicker)

	model := s.modelForSeries(series)

	// Markov fair value for home player
	fvHome := model.FairValue(
		setsHome, setsAway,
		p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints,
		p.Server, p.IsTiebreak,
	)
	fvAway := 1.0 - fvHome

	// Compute edges for both markets, fire only the larger positive one.
	type sideEdge struct {
		idx        int
		mkt        string
		price      float64
		fv         float64
		edgeCents  int
	}
	var candidates []sideEdge

	for i, mkt := range mkts {
		fv := fvHome
		if i == 1 {
			fv = fvAway
		}

		s.mu.RLock()
		price := s.prices[mkt]
		priceTime := s.priceTimes[mkt]
		lastFire := s.lastFire[mkt]
		s.mu.RUnlock()

		if price <= 0 || now.Sub(priceTime) > priceStaleTTL {
			continue
		}

		// Per-market cooldown — no stacking.
		if s.cfg.CooldownSeconds > 0 && !lastFire.IsZero() {
			if now.Sub(lastFire).Seconds() < float64(s.cfg.CooldownSeconds) {
				continue
			}
		}

		edgeCents := int((fv - price) * 100)
		if edgeCents < s.cfg.MinEdgeCents {
			continue
		}
		if price < s.cfg.MinMarketPrice {
			continue
		}
		if s.cfg.MaxMarketPrice > 0 && price > s.cfg.MaxMarketPrice {
			continue
		}

		candidates = append(candidates, sideEdge{i, mkt, price, fv, edgeCents})
	}

	// Cross-market guard: fire only the larger edge. Prevents buying both
	// sides when spread makes both edges positive.
	if len(candidates) == 0 {
		return
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.edgeCents > best.edgeCents {
			best = c
		}
	}

	size := kellySized(best.fv, best.price)

	s.mu.Lock()
	s.lastFire[best.mkt] = now
	s.mu.Unlock()

	s.emitter.EmitOrder(store.Order{
		TS:            now.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  best.mkt,
		Action:        "buy",
		Context:       fmt.Sprintf("convex_%s_set%d_game%d_pt%d", sideName(best.idx+1), p.SetNumber, p.GameNumber, p.PointNumber),
		ConvProb:      best.fv,
		MarketPrice:   best.price,
		EdgeCents:     best.edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     p.SetNumber,
		Strategy:      s.cfg.Label,
	})

	s.log.Debug("convex pool signal",
		"event", eventTicker, "market", best.mkt,
		"fv", best.fv, "price", best.price, "edge", best.edgeCents,
		"series", series)
}

func (s *ConvexPoolStrategy) getSetsHome(eventTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms := s.states[eventTicker]; ms != nil {
		return ms.setsHome
	}
	return 0
}

func (s *ConvexPoolStrategy) getSetsAway(eventTicker string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ms := s.states[eventTicker]; ms != nil {
		return ms.setsAway
	}
	return 0
}

func (s *ConvexPoolStrategy) String() string {
	return fmt.Sprintf("ConvexPoolStrategy{markets=%d}", len(s.markets))
}

func (s *ConvexPoolStrategy) OnTick(ctx context.Context) {}
