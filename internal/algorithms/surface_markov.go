package algorithms

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// SurfaceMarkovConfig controls the surface-aware Markov strategy (RQ6).
// Replaces global pServe=0.64 with surface + series specific serve rates.
// Empirical hold rates from 4 days of data:
//
//	ATP main:       61.3%   WTA main:       52.0%
//	ATP Challenger: 61.0%   WTA Challenger: 52.3%
//	ITF men:        57.9%   ITF women:      54.0%
//
// Surface adjustments (literature + partial data):
//
//	clay:  -4pp vs base   (slower, more breaks)
//	hard:   0pp           (baseline)
//	grass: +6pp           (faster, fewer breaks)
//
// Strategy: compute Markov fair value with calibrated pServe, trade
// when |fair_value - market_price| > threshold. Same structure as
// calibrated_markov but uses a lookup table instead of logistic model.
type SurfaceMarkovConfig struct {
	// MinEdgeCents: minimum edge to fire (default 3)
	MinEdgeCents int
	// MaxMarketPrice: max price to buy at (default 0.85)
	MaxMarketPrice float64
	// SuggestedSize: order size in dollars
	SuggestedSize float64
	// Label: strategy label
	Label string
}

func DefaultSurfaceMarkovConfig() SurfaceMarkovConfig {
	return SurfaceMarkovConfig{
		MinEdgeCents:   3,
		MaxMarketPrice: 0.85,
		SuggestedSize:  10.0,
		Label:          "surface-markov",
	}
}

// seriesBasePServe maps series_ticker to base serve-win probability.
// Derived from empirical hold rates in EXPLORATORY_QUESTIONS.md RQ6.
var seriesBasePServe = map[string]float64{
	"KXATPMATCH":           0.613,
	"KXWTAMATCH":           0.520,
	"KXATPCHALLENGERMATCH": 0.610,
	"KXWTACHALLENGERMATCH": 0.523,
	"KXITFMATCH":           0.579,
	"KXITFWMATCH":          0.540,
	"KXATPDOUBLES":         0.560, // doubles: lower hold
	"KXWTADOUBLES":         0.500,
	"KXITFDOUBLES":         0.530,
	"KXITFWDOUBLES":        0.490,
	"KXTENNISEXHIBITION":   0.580,
	"KXCHALLENGERMATCH":    0.580,
}

// surfaceAdjustment returns serve-win probability adjustment by surface.
func surfaceAdjustment(surface string) float64 {
	switch surface {
	case "clay":
		return -0.04
	case "grass":
		return 0.06
	case "hard", "hard (indoor)":
		return 0.0
	default:
		return 0.0
	}
}

// pServeForContext computes serve-win probability from series + surface.
func pServeForContext(series, surface string) float64 {
	base := seriesBasePServe[series]
	if base == 0 {
		base = defaultPServe
	}
	p := base + surfaceAdjustment(surface)
	if p < 0.40 {
		p = 0.40
	}
	if p > 0.75 {
		p = 0.75
	}
	return p
}

// SurfaceMarkovStrategy uses surface + series calibrated pServe in the
// Markov model. Trades when market diverges from calibrated fair value.
//
// Needs series_ticker (SeriesSetter) and surface (SurfaceSetter).
// In live mode, auto-loads both from DB.
type SurfaceMarkovStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool
	series    map[string]string // event_ticker -> series_ticker
	surfaces  map[string]string // event_ticker -> surface
	emitter   OrderEmitter
	db        *store.DB
	log       *slog.Logger
	cfg       SurfaceMarkovConfig
	replayNow *time.Time
}

func NewSurfaceMarkovStrategy(emitter OrderEmitter, log *slog.Logger, cfg SurfaceMarkovConfig) *SurfaceMarkovStrategy {
	return &SurfaceMarkovStrategy{
		prices:   make(map[string]float64),
		markets:  make(map[string][]string),
		fired:    make(map[string]bool),
		series:   make(map[string]string),
		surfaces: make(map[string]string),
		emitter:  emitter,
		log:      log,
		cfg:      cfg,
	}
}

func NewSurfaceMarkovStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg SurfaceMarkovConfig) *SurfaceMarkovStrategy {
	s := NewSurfaceMarkovStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *SurfaceMarkovStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
	if s.db != nil {
		s.loadContext(eventTicker)
	}
}

func (s *SurfaceMarkovStrategy) loadContext(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
	surface, err := s.db.GetSurface(context.Background(), eventTicker)
	if err == nil && surface != "" {
		s.mu.Lock()
		s.surfaces[eventTicker] = surface
		s.mu.Unlock()
	}
}

// SetSeriesTicker sets the series ticker for an event (backtest wiring).
func (s *SurfaceMarkovStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

// SetSurface sets the surface for an event (backtest wiring).
func (s *SurfaceMarkovStrategy) SetSurface(eventTicker, surface string) {
	s.mu.Lock()
	s.surfaces[eventTicker] = surface
	s.mu.Unlock()
}

func (s *SurfaceMarkovStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.series, eventTicker)
	delete(s.surfaces, eventTicker)
	s.mu.Unlock()
}

func (s *SurfaceMarkovStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *SurfaceMarkovStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.mu.Unlock()
}

func (s *SurfaceMarkovStrategy) OnPoint(eventTicker string, p store.Point) {
	s.mu.Lock()
	if s.fired[eventTicker] {
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
	series := s.series[eventTicker]
	surface := s.surfaces[eventTicker]
	s.mu.Unlock()

	if homePrice <= 0 && awayPrice <= 0 {
		return
	}

	pServe := pServeForContext(series, surface)
	markov := NewMarkovModelWithProb(pServe)

	setsHome := 0
	setsAway := 0
	if p.HomeSetGames > p.AwaySetGames {
		setsHome = 1
	} else if p.AwaySetGames > p.HomeSetGames {
		setsAway = 1
	}

	homeFV := markov.WinProbability(
		setsHome, setsAway, p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints, p.Server, p.IsTiebreak,
	)
	homeFV = math.Max(0.01, math.Min(0.99, homeFV))
	awayFV := 1.0 - homeFV

	s.checkEdge(eventTicker, homeMkt, homePrice, homeFV, p.SetNumber)
	s.checkEdge(eventTicker, awayMkt, awayPrice, awayFV, p.SetNumber)
}

func (s *SurfaceMarkovStrategy) checkEdge(eventTicker, mkt string, marketPrice, fairValue float64, setNum int) {
	if marketPrice <= 0 || marketPrice > s.cfg.MaxMarketPrice {
		return
	}
	edgeCents := int((fairValue-marketPrice)*100 + 1e-9)
	if edgeCents < s.cfg.MinEdgeCents {
		return
	}

	s.mu.Lock()
	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}
	s.fired[eventTicker] = true
	s.mu.Unlock()

	ts := s.now()
	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  mkt,
		Action:        "buy",
		Context:       fmt.Sprintf("smarkov_set%d_edge%dc", setNum, edgeCents),
		ConvProb:      fairValue,
		MarketPrice:   marketPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: kellySized(fairValue, marketPrice),
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     setNum,
		Strategy:      s.cfg.Label,
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("surface-markov: order dropped", "match", eventTicker, "market", mkt)
		return
	}
	s.log.Info("surface-markov: order emitted",
		"match", eventTicker, "market", mkt,
		"price", marketPrice, "fair_value", fairValue,
		"edge_cents", edgeCents)
}

func (s *SurfaceMarkovStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *SurfaceMarkovStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *SurfaceMarkovStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *SurfaceMarkovStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("SurfaceMarkovStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

func (s *SurfaceMarkovStrategy) PreMatchGated() {}

func (s *SurfaceMarkovStrategy) OnTick(ctx context.Context) {}
