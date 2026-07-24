package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// FadeLongshotConfig controls the fade-longshot strategy.
// Data: at T-10min, favorite with price >= 50c won 100% (n=71, +10.4c edge).
// At T-10min, price >= 85c: 100% hit, +4.5c edge, Sharpe 1.01.
type FadeLongshotConfig struct {
	// WindowSeconds: how many seconds before close to enter.
	WindowSeconds int
	// MinPrice: minimum favorite price to enter (0 = no filter).
	MinPrice float64
	// MaxPrice: maximum favorite price to enter (0 = no cap).
	MaxPrice float64
	// BaseSize: base order size in dollars.
	BaseSize float64
	// Label: strategy name for logging.
	Label string
	// DynamicConvProb: if true, convProb is derived from live score context
	// (set/game lead, match/set point) received via OnPoint. Falls back to
	// fixed 0.99 when no score data is available.
	DynamicConvProb bool
	// SeriesFilter: if non-empty, only fire on events matching one of these
	// series tickers. Used for RQ3 (series-tier stratification) and RQ13
	// (doubles-only variant). Empty = no filter (all series).
	SeriesFilter []string
	// UTCHourStart / UTCHourEnd: if non-zero, only fire when close_ts UTC hour
	// falls in [Start, End). Used for RQ10 (time-of-day stratification).
	// UTCHourEnd=0 means no end limit. Both 0 = no filter.
	UTCHourStart int
	UTCHourEnd   int
}

func DefaultFadeLongshotConfig() FadeLongshotConfig {
	return FadeLongshotConfig{
		WindowSeconds:   900,
		MinPrice:        0.50,
		MaxPrice:        0.0,
		BaseSize:        10.0,
		Label:           "fadelongshot",
		DynamicConvProb: true,
	}
}

// fadeScoreState tracks live score context for dynamic convProb + pace estimation.
type fadeScoreState struct {
	homeSetWins  int
	awaySetWins  int
	homeGames    int
	awayGames    int
	setNumber    int
	isMatchPoint bool
	isSetPoint   bool
	firstPointTS int64 // unix ms of first scored point (match start proxy)
	pointsPlayed int   // running count of points received
}

// FadeLongshotStrategy buys the favorite (higher-priced YES) when the match
// is estimated to be within WindowSeconds of ending. Entry timing uses a
// pace estimator (points played / elapsed time + score state) instead of
// Kalshi's close_ts, which is a placeholder until match end.
//
// Implements ScoreObserver for pace estimation + dynamic convProb.
// Does NOT implement CloseTimeStrategy — uses the interleaved replay path
// in backtest (ticks + points merged by timestamp).
type FadeLongshotStrategy struct {
	mu         sync.RWMutex
	prices     map[string]float64
	priceTimes map[string]time.Time
	markets    map[string][]string
	fired      map[string]bool
	scores     map[string]*fadeScoreState
	series     map[string]string // event_ticker -> series_ticker
	emitter    OrderEmitter
	db         *store.DB // nil in backtest mode
	log        *slog.Logger
	cfg        FadeLongshotConfig
	replayNow  *time.Time
}

func NewFadeLongshotStrategy(emitter OrderEmitter, log *slog.Logger, cfg FadeLongshotConfig) *FadeLongshotStrategy {
	return &FadeLongshotStrategy{
		prices:     make(map[string]float64),
		priceTimes: make(map[string]time.Time),
		markets:    make(map[string][]string),
		fired:      make(map[string]bool),
		scores:     make(map[string]*fadeScoreState),
		series:     make(map[string]string),
		emitter:    emitter,
		log:        log,
		cfg:        cfg,
	}
}

// NewFadeLongshotStrategyWithDB creates a live-mode fadelongshot that
// auto-loads series from the markets table on RegisterMarkets.
func NewFadeLongshotStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg FadeLongshotConfig) *FadeLongshotStrategy {
	s := NewFadeLongshotStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *FadeLongshotStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()

	// Live mode: load series from DB for series-filtered variants
	if s.db != nil {
		s.loadSeriesTicker(eventTicker)
	}
}

func (s *FadeLongshotStrategy) loadSeriesTicker(eventTicker string) {
	series, err := s.db.GetSeriesTicker(context.Background(), eventTicker)
	if err == nil && series != "" {
		s.mu.Lock()
		s.series[eventTicker] = series
		s.mu.Unlock()
	}
}

// SetSeriesTicker maps event_ticker to series_ticker for series filtering.
// Called by backtest engine or live wiring.
func (s *FadeLongshotStrategy) SetSeriesTicker(eventTicker, seriesTicker string) {
	s.mu.Lock()
	s.series[eventTicker] = seriesTicker
	s.mu.Unlock()
}

// RegisterCloseTime removed — pace estimator replaces close_ts for entry timing.
// fadelongshot no longer implements CloseTimeStrategy; backtest uses the
// interleaved replay path (ticks + points) instead.

func (s *FadeLongshotStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	for _, mkt := range s.markets[eventTicker] {
		delete(s.prices, mkt)
		delete(s.priceTimes, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.fired, eventTicker)
	delete(s.scores, eventTicker)
	delete(s.series, eventTicker)
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) OnPrice(marketTicker string, price float64) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = s.now()
	s.mu.Unlock()
	s.checkEntry(marketTicker)
}

func (s *FadeLongshotStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.priceTimes[marketTicker] = ts
	s.mu.Unlock()
	s.checkEntryAt(marketTicker, ts)
}

// OnPoint updates live score context for dynamic convProb calculation.
// Implements ScoreObserver so MultiStrategyRuntime fans out point events.
func (s *FadeLongshotStrategy) OnPoint(eventTicker string, p store.Point) {
	s.mu.Lock()
	ms, ok := s.scores[eventTicker]
	if !ok {
		ms = &fadeScoreState{}
		s.scores[eventTicker] = ms
	}
	ms.homeSetWins = p.HomeSetGames
	ms.awaySetWins = p.AwaySetGames
	ms.homeGames = p.HomeGames
	ms.awayGames = p.AwayGames
	ms.setNumber = p.SetNumber
	ms.isMatchPoint = p.IsMatchPoint
	ms.isSetPoint = p.IsSetPoint
	ms.pointsPlayed++
	if ms.firstPointTS == 0 && p.TS > 0 {
		ms.firstPointTS = p.TS
	}
	s.mu.Unlock()

	// Re-check entry after score update — convProb or pace may have changed
	s.checkEntryForEvent(eventTicker)
}

func (s *FadeLongshotStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

// dynamicConvProb estimates conversion probability from live score context.
// Higher when favorite has set/game lead, serving, or at match/set point.
// Falls back to fixed 0.99 when no score data is available.
func (s *FadeLongshotStrategy) dynamicConvProb(eventTicker string, favPrice float64) float64 {
	s.mu.RLock()
	ms, ok := s.scores[eventTicker]
	s.mu.RUnlock()
	if !ok || ms == nil {
		return 0.99
	}

	prob := 0.90 // base for favorite in final minutes

	// Set lead: +3c per set lead
	setLead := ms.homeSetWins - ms.awaySetWins
	if setLead < 0 {
		setLead = -setLead
	}
	prob += float64(setLead) * 0.03

	// Game lead in current set: +1c per game
	gameLead := ms.homeGames - ms.awayGames
	if gameLead < 0 {
		gameLead = -gameLead
	}
	prob += float64(gameLead) * 0.01

	// Match point: near-certain conversion
	if ms.isMatchPoint {
		prob = 0.995
	}

	// Set point: high conversion
	if ms.isSetPoint && !ms.isMatchPoint {
		prob = math.Max(prob, 0.97)
	}

	// Clamp: must stay above favPrice to have edge, cap at 0.999
	if prob <= favPrice {
		prob = favPrice + 0.01
	}
	if prob > 0.999 {
		prob = 0.999
	}

	return prob
}

// estimateRemainingMin predicts minutes until match end from score state + pace.
// Returns 999 (never fire) when score data is unavailable or match is too early.
// Calibrated against 10 weeks of backtest data: at T-15min before close,
// set 2 matches avg 97 pts / 71 min (1.36 pts/min), set 3 matches avg 189 pts /
// 113 min (1.67 pts/min). Remaining-points table derived from games leader
// in current set.
// Caller must NOT hold s.mu.
func (s *FadeLongshotStrategy) estimateRemainingMin(eventTicker string, now time.Time) float64 {
	s.mu.RLock()
	ms := s.scores[eventTicker]
	s.mu.RUnlock()
	return estimateRemainingMinLocked(ms, now)
}

// estimateRemainingMinLocked computes remaining minutes from a score state
// snapshot. Safe to call while holding s.mu.
func estimateRemainingMinLocked(ms *fadeScoreState, now time.Time) float64 {
	if ms == nil || ms.firstPointTS == 0 {
		return 999
	}
	elapsedMin := float64(now.UnixMilli()-ms.firstPointTS) / 60000.0
	if elapsedMin < 1 {
		return 999
	}
	pace := float64(ms.pointsPlayed) / elapsedMin
	if pace <= 0 {
		return 999
	}
	return remainingPoints(ms) / pace
}

// remainingPoints estimates points left until match end from score state.
// Match point → 0 (imminent). Set 1 → 999 (too early, unpredictable).
// Set 2/3 → scaled by games leader in current set.
func remainingPoints(ms *fadeScoreState) float64 {
	if ms.isMatchPoint {
		return 0
	}
	if ms.setNumber <= 1 {
		return 999
	}
	leaderGames := ms.homeGames
	if ms.awayGames > leaderGames {
		leaderGames = ms.awayGames
	}
	if ms.setNumber == 2 {
		switch {
		case leaderGames >= 5:
			return 10
		case leaderGames >= 3:
			return 20
		default:
			return 35
		}
	}
	// set 3+ (deciding set)
	switch {
	case leaderGames >= 5:
		return 8
	case leaderGames >= 3:
		return 15
	default:
		return 25
	}
}

func (s *FadeLongshotStrategy) checkEntry(marketTicker string) {
	s.checkEntryAt(marketTicker, s.now())
}

// checkEntryForEvent re-evaluates entry after a score update. Called by
// OnPoint when score context changes (e.g. match point reached).
func (s *FadeLongshotStrategy) checkEntryForEvent(eventTicker string) {
	s.mu.RLock()
	mkts, ok := s.markets[eventTicker]
	s.mu.RUnlock()
	if !ok || len(mkts) < 2 {
		return
	}
	// Check entry from both markets' perspective
	for _, mkt := range mkts {
		s.checkEntryAt(mkt, s.now())
	}
}

func (s *FadeLongshotStrategy) checkEntryAt(marketTicker string, ts time.Time) {
	s.mu.Lock()

	eventTicker := ""
	for et, mkts := range s.markets {
		for _, m := range mkts {
			if m == marketTicker {
				eventTicker = et
				break
			}
		}
	}
	if eventTicker == "" {
		s.mu.Unlock()
		return
	}

	if s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	// Pace estimator: fire when estimated remaining time <= WindowSeconds.
	// Replaces close_ts dependency (Kalshi's initial close_ts is a placeholder
	// updated only at match end via close_date_updated lifecycle event).
	ms := s.scores[eventTicker]
	remainingMin := estimateRemainingMinLocked(ms, ts)
	windowMin := float64(s.cfg.WindowSeconds) / 60.0
	if remainingMin > windowMin {
		s.mu.Unlock()
		return
	}

	// Series filter: skip events not in SeriesFilter list
	if len(s.cfg.SeriesFilter) > 0 {
		series := s.series[eventTicker]
		if series == "" {
			s.mu.Unlock()
			return
		}
		matched := false
		for _, sf := range s.cfg.SeriesFilter {
			if series == sf {
				matched = true
				break
			}
		}
		if !matched {
			s.mu.Unlock()
			return
		}
	}

	// UTC hour filter: use current time (within ~15 min of actual close)
	if s.cfg.UTCHourStart > 0 || s.cfg.UTCHourEnd > 0 {
		curUTC := ts.UTC().Hour()
		start := s.cfg.UTCHourStart
		end := s.cfg.UTCHourEnd
		if end == 0 {
			end = 24
		}
		inWindow := false
		if start <= end {
			inWindow = curUTC >= start && curUTC < end
		} else {
			inWindow = curUTC >= start || curUTC < end
		}
		if !inWindow {
			s.mu.Unlock()
			return
		}
	}

	mkts := s.markets[eventTicker]
	if len(mkts) < 2 {
		s.mu.Unlock()
		return
	}

	price := s.prices[marketTicker]
	if price <= 0 {
		s.mu.Unlock()
		return
	}

	otherMkt := ""
	otherPrice := 0.0
	for _, m := range mkts {
		if m != marketTicker {
			otherMkt = m
			otherPrice = s.prices[m]
			break
		}
	}

	if otherPrice <= 0 {
		s.mu.Unlock()
		return
	}

	favMkt := marketTicker
	favPrice := price
	if otherPrice > price {
		favMkt = otherMkt
		favPrice = otherPrice
	}

	if favPrice < s.cfg.MinPrice {
		s.mu.Unlock()
		return
	}
	if s.cfg.MaxPrice > 0 && favPrice > s.cfg.MaxPrice {
		s.mu.Unlock()
		return
	}

	s.mu.Unlock()

	convProb := 0.99
	if s.cfg.DynamicConvProb {
		convProb = s.dynamicConvProb(eventTicker, favPrice)
	}
	edgeCents := int((convProb-favPrice)*100 + 1e-9)
	if edgeCents < 1 {
		return
	}

	size := kellySized(convProb, favPrice)

	payload, _ := json.Marshal(map[string]any{
		"window_s":     s.cfg.WindowSeconds,
		"remaining_min": remainingMin,
		"entry_ts":     ts.UnixMilli(),
		"fav_price":    favPrice,
		"other_price":  otherPrice,
		"conv_prob":    convProb,
		"dynamic":      s.cfg.DynamicConvProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  favMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("fade_longshot_T-%ds", s.cfg.WindowSeconds),
		ConvProb:      convProb,
		MarketPrice:   favPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		Bankroll:      paperBankroll,
		KellyFraction: kellyFractionP,
		SetNumber:     0,
		Strategy:      "fadelongshot",
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("fadelongshot: order dropped", "match", eventTicker, "market", favMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("fadelongshot: order emitted",
		"match", eventTicker, "market", favMkt,
		"price", favPrice, "edge_cents", edgeCents, "size", size)
}

func (s *FadeLongshotStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.priceTimes, marketTicker)
	s.mu.Unlock()
}

func (s *FadeLongshotStrategy) GetPrice(marketTicker string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[marketTicker]
}

func (s *FadeLongshotStrategy) GetPriceAge(marketTicker string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (s *FadeLongshotStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("FadeLongshotStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}

func (s *FadeLongshotStrategy) OnTick(ctx context.Context) {}
