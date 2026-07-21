package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// CalibratedMarkovConfig controls the calibrated Markov strategy.
// Loads serve-win logistic regression weights from JSON (trained offline
// by research/ml/train_serve_win.py). Uses calibrated pServe per context
// instead of global 0.64. Computes Markov fair value, trades when
// |market - fair_value| > threshold.
type CalibratedMarkovConfig struct {
	// ModelPath: path to serve_win_logistic.json
	ModelPath string
	// MinEdgeCents: minimum edge to fire (default 3)
	MinEdgeCents int
	// MaxMarketPrice: max price to buy at (default 0.85)
	MaxMarketPrice float64
	// SuggestedSize: order size in dollars
	SuggestedSize float64
	// Label: strategy label
	Label string
}

func DefaultCalibratedMarkovConfig() CalibratedMarkovConfig {
	return CalibratedMarkovConfig{
		ModelPath:      "research/ml/models/serve_win_logistic.json",
		MinEdgeCents:   3,
		MaxMarketPrice: 0.85,
		SuggestedSize:  10.0,
		Label:          "calibrated-markov",
	}
}

// serveWinModel holds logistic regression weights loaded from JSON.
type serveWinModel struct {
	Coef        []float64      `json:"coef"`
	Intercept   float64        `json:"intercept"`
	SeriesMap   map[string]int `json:"series_map"`
	PointMap    map[string]int `json:"point_map"`
	OverallRate float64        `json:"overall_rate"`
}

// CalibratedMarkovStrategy uses ML-calibrated serve-win probability
// to compute Markov fair value. Trades when market diverges from
// calibrated fair value by more than threshold.
//
// Model trained by research/ml/train_serve_win.py on 56k points.
// Replaces global pServe=0.64 with per-context estimate.
type CalibratedMarkovStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	fired     map[string]bool
	series    map[string]string // event_ticker -> series_ticker
	emitter   OrderEmitter
	db        *store.DB // nil in backtest mode
	log       *slog.Logger
	cfg       CalibratedMarkovConfig
	model     *serveWinModel
	markov    *MarkovModel
	replayNow *time.Time
}

func NewCalibratedMarkovStrategy(emitter OrderEmitter, log *slog.Logger, cfg CalibratedMarkovConfig) *CalibratedMarkovStrategy {
	s := &CalibratedMarkovStrategy{
		prices:  make(map[string]float64),
		markets: make(map[string][]string),
		fired:   make(map[string]bool),
		series:  make(map[string]string),
		emitter: emitter,
		log:     log,
		cfg:     cfg,
	}
	if err := s.loadModel(); err != nil {
		log.Error("calibrated-markov: failed to load model", "err", err, "path", cfg.ModelPath)
		s.markov = NewMarkovModel()
	} else {
		log.Info("calibrated-markov: model loaded",
			"path", cfg.ModelPath, "features", len(s.model.Coef))
		s.markov = NewMarkovModel()
	}
	return s
}

// NewCalibratedMarkovStrategyWithDB creates a live-mode variant that
// auto-loads series_ticker from the events table on RegisterMarkets.
func NewCalibratedMarkovStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg CalibratedMarkovConfig) *CalibratedMarkovStrategy {
	s := NewCalibratedMarkovStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *CalibratedMarkovStrategy) loadModel() error {
	data, err := os.ReadFile(s.cfg.ModelPath)
	if err != nil {
		return fmt.Errorf("read model: %w", err)
	}
	var m serveWinModel
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	s.model = &m
	return nil
}

// sigmoid computes logistic function.
func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

// predictServeWin computes P(server wins point | context) using logistic model.
func (s *CalibratedMarkovStrategy) predictServeWin(seriesTicker string, server, homeGames, awayGames int, homePoints, awayPoints string, isBP, isTB bool) float64 {
	if s.model == nil {
		return defaultPServe
	}
	seriesID, ok := s.model.SeriesMap[seriesTicker]
	if !ok {
		seriesID = -1
	}
	hp := s.model.PointMap[homePoints]
	if hp == 0 && homePoints != "0" {
		hp = 4 // A
	}
	ap := s.model.PointMap[awayPoints]
	if ap == 0 && awayPoints != "0" {
		ap = 4
	}
	serverIsHome := 0
	if server == 1 {
		serverIsHome = 1
	}
	isBPInt := 0
	if isBP {
		isBPInt = 1
	}
	isTBInt := 0
	if isTB {
		isTBInt = 1
	}
	pointDiff := hp - ap
	gameDiff := homeGames - awayGames

	// Feature order must match training:
	// series_id, server, home_games, away_games, point_diff, game_diff,
	// is_bp, is_tb, server_is_home, hp, ap
	features := []float64{
		float64(seriesID), float64(server), float64(homeGames), float64(awayGames),
		float64(pointDiff), float64(gameDiff),
		float64(isBPInt), float64(isTBInt),
		float64(serverIsHome), float64(hp), float64(ap),
	}

	z := s.model.Intercept
	for i, f := range features {
		if i < len(s.model.Coef) {
			z += s.model.Coef[i] * f
		}
	}
	return sigmoid(z)
}

func (s *CalibratedMarkovStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()

	// Live mode: auto-load series_ticker from DB
	if s.db != nil {
		s.loadSeriesTicker(eventTicker)
	}
}

func (s *CalibratedMarkovStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err != nil {
		s.log.Warn("calibrated-markov: no series_ticker", "event", eventTicker, "err", err)
		return
	}
	s.mu.Lock()
	s.series[eventTicker] = series
	s.mu.Unlock()
}

func (s *CalibratedMarkovStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *CalibratedMarkovStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *CalibratedMarkovStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.mu.Unlock()
}

// OnPoint updates score state and checks for edge.
func (s *CalibratedMarkovStrategy) OnPoint(eventTicker string, p store.Point) {
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
	s.mu.Unlock()

	if homePrice <= 0 && awayPrice <= 0 {
		return
	}

	// Get series ticker for serve-win model
	seriesTicker := s.series[eventTicker]

	// Compute calibrated pServe for this context
	calibratedPServe := s.predictServeWin(
		seriesTicker, p.Server, p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints, p.IsBreakPoint, p.IsTiebreak,
	)

	// Build calibrated Markov model
	calibratedMarkov := NewMarkovModelWithProb(calibratedPServe)

	// Compute sets won from set games
	setsHome := 0
	setsAway := 0
	if p.HomeSetGames > p.AwaySetGames {
		setsHome = 1
	} else if p.AwaySetGames > p.HomeSetGames {
		setsAway = 1
	}

	// Fair value for home player
	homeFV := calibratedMarkov.WinProbability(
		setsHome, setsAway, p.HomeGames, p.AwayGames,
		p.HomePoints, p.AwayPoints, p.Server, p.IsTiebreak,
	)
	// Fair value for away player
	awayFV := 1.0 - homeFV

	// Check edge on both markets
	s.checkEdge(eventTicker, homeMkt, homePrice, homeFV, p.SetNumber)
	s.checkEdge(eventTicker, awayMkt, awayPrice, awayFV, p.SetNumber)
}

func (s *CalibratedMarkovStrategy) checkEdge(eventTicker, mkt string, marketPrice, fairValue float64, setNum int) {
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
		Context:       fmt.Sprintf("calmarkov_set%d_edge%dc", setNum, edgeCents),
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
		s.log.Warn("calibrated-markov: order dropped", "match", eventTicker, "market", mkt)
		return
	}
	s.log.Info("calibrated-markov: order emitted",
		"match", eventTicker, "market", mkt,
		"price", marketPrice, "fair_value", fairValue,
		"edge_cents", edgeCents)
}

// SetSeriesTicker maps event_ticker to series_ticker for model input.
// Called by live ingestion or backtest setup.
func (s *CalibratedMarkovStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

func (s *CalibratedMarkovStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *CalibratedMarkovStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *CalibratedMarkovStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	s.mu.Unlock()
}

func (s *CalibratedMarkovStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("CalibratedMarkovStrategy{%s: markets=%d, fired=%d, model_loaded=%v}",
		s.cfg.Label, len(s.markets), len(s.fired), s.model != nil)
}

func (s *CalibratedMarkovStrategy) PreMatchGated() {}

func (s *CalibratedMarkovStrategy) OnTick(ctx context.Context) {}
