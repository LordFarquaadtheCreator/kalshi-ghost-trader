package algorithms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// VolumeRatioConfig controls the volume-ratio pre-match strategy (RQ14).
// Hypothesis: if dollar_volume on player A's YES is much higher than
// player B's YES, sharp money favours A. If A wins more often than
// price implies, the volume ratio is a predictive signal.
//
// Entry: at T-WindowSeconds before close, compare cumulative
// dollar_volume between the two YES markets. Buy the higher-volume
// side if its price is below MinPrice (underdog with heavy money) or
// above the other side's price by a margin (favourite with heavy money).
type VolumeRatioConfig struct {
	// WindowSeconds: seconds before close to enter.
	WindowSeconds int
	// MinVolumeRatio: ratio of heavy-side vol to light-side vol to
	// trigger (e.g. 2.0 = heavy side has 2x the volume).
	MinVolumeRatio float64
	// MinPrice: minimum price to enter (skip near-zero longshots).
	MinPrice float64
	// MaxPrice: maximum price to enter (skip near-certainties).
	MaxPrice float64
	// BaseSize: order size in dollars.
	BaseSize float64
	// Label: strategy label.
	Label string
}

func DefaultVolumeRatioConfig() VolumeRatioConfig {
	return VolumeRatioConfig{
		WindowSeconds:   600,
		MinVolumeRatio:  2.0,
		MinPrice:        0.10,
		MaxPrice:        0.90,
		BaseSize:        10.0,
		Label:           "volratio",
	}
}

// VolumeRatioStrategy buys the side with higher cumulative dollar_volume
// at T-WindowSeconds before close. Tests whether heavy money predicts
// outcome better than price.
//
// Needs close_ts (CloseTimeStrategy) and dollar_volume per market
// (VolumeSetter). In live mode, queries DB for latest dollar_volume.
type VolumeRatioStrategy struct {
	mu        sync.RWMutex
	prices    map[string]float64
	markets   map[string][]string
	closeTs   map[string]int64
	fired     map[string]bool
	volumes   map[string]float64 // market_ticker -> latest dollar_volume
	volSeries map[string][]TickVolume
	emitter   OrderEmitter
	db        *store.DB
	log       *slog.Logger
	cfg       VolumeRatioConfig
	replayNow *time.Time
}

// TickVolume is a timestamped dollar_volume sample for backtest replay.
type TickVolume struct {
	TS           int64
	DollarVolume float64
}

func NewVolumeRatioStrategy(emitter OrderEmitter, log *slog.Logger, cfg VolumeRatioConfig) *VolumeRatioStrategy {
	return &VolumeRatioStrategy{
		prices:    make(map[string]float64),
		markets:   make(map[string][]string),
		closeTs:   make(map[string]int64),
		fired:     make(map[string]bool),
		volumes:   make(map[string]float64),
		volSeries: make(map[string][]TickVolume),
		emitter:   emitter,
		log:       log,
		cfg:       cfg,
	}
}

func NewVolumeRatioStrategyWithDB(emitter OrderEmitter, db *store.DB, log *slog.Logger, cfg VolumeRatioConfig) *VolumeRatioStrategy {
	s := NewVolumeRatioStrategy(emitter, log, cfg)
	s.db = db
	return s
}

func (s *VolumeRatioStrategy) RegisterMarkets(eventTicker string, marketTickers []string) {
	s.mu.Lock()
	s.markets[eventTicker] = marketTickers
	s.mu.Unlock()
	if s.db != nil {
		s.loadCloseTime(eventTicker)
	}
}

func (s *VolumeRatioStrategy) loadCloseTime(eventTicker string) {
	mkts, err := s.db.GetMarketsByEvent(context.Background(), eventTicker)
	if err != nil {
		return
	}
	for _, m := range mkts {
		if m.CloseTS > 0 {
			s.mu.Lock()
			s.closeTs[eventTicker] = m.CloseTS
			s.mu.Unlock()
			return
		}
	}
}

func (s *VolumeRatioStrategy) RegisterCloseTime(eventTicker string, closeTs int64) {
	s.mu.Lock()
	s.closeTs[eventTicker] = closeTs
	s.mu.Unlock()
}

// SetVolumeSeries sets the dollar_volume time series for a market.
// Used by backtest engine to avoid lookahead bias — the strategy
// looks up volume at entry time via binary search.
func (s *VolumeRatioStrategy) SetVolumeSeries(marketTicker string, vols []TickVolume) {
	s.mu.Lock()
	s.volSeries[marketTicker] = vols
	s.mu.Unlock()
}

// volumeAtTime returns dollar_volume at or before the given timestamp
// using binary search on the pre-loaded volume series.
func (s *VolumeRatioStrategy) volumeAtTime(marketTicker string, ts int64) float64 {
	s.mu.RLock()
	series := s.volSeries[marketTicker]
	s.mu.RUnlock()
	if len(series) == 0 {
		return 0
	}
	// Binary search for last entry with series[i].ts <= ts
	idx := sort.Search(len(series), func(i int) bool {
		return series[i].TS > ts
	})
	if idx == 0 {
		return 0
	}
	return series[idx-1].DollarVolume
}

func (s *VolumeRatioStrategy) UnregisterMarkets(eventTicker string) {
	s.mu.Lock()
	mkts := s.markets[eventTicker]
	for _, mkt := range mkts {
		delete(s.prices, mkt)
		delete(s.volumes, mkt)
		delete(s.volSeries, mkt)
	}
	delete(s.markets, eventTicker)
	delete(s.closeTs, eventTicker)
	delete(s.fired, eventTicker)
	s.mu.Unlock()
}

func (s *VolumeRatioStrategy) OnPrice(marketTicker string, price float64) {
	s.OnPriceAt(marketTicker, price, s.now())
}

func (s *VolumeRatioStrategy) OnPriceAt(marketTicker string, price float64, ts time.Time) {
	s.mu.Lock()
	s.prices[marketTicker] = price
	s.mu.Unlock()
	s.checkEntry(marketTicker, ts)
}

func (s *VolumeRatioStrategy) checkEntry(marketTicker string, ts time.Time) {
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
	if eventTicker == "" || s.fired[eventTicker] {
		s.mu.Unlock()
		return
	}

	closeTs, ok := s.closeTs[eventTicker]
	if !ok || closeTs == 0 {
		s.mu.Unlock()
		if s.db != nil {
			s.loadCloseTime(eventTicker)
		}
		return
	}

	entryWindow := int64(s.cfg.WindowSeconds) * 1000
	entryTs := closeTs - entryWindow
	if ts.UnixMilli() < entryTs {
		s.mu.Unlock()
		return
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
	s.mu.Unlock()

	// Get dollar_volume at entry time for both markets
	entryUnix := ts.UnixMilli()
	volA := s.volumeAtTime(marketTicker, entryUnix)
	volB := s.volumeAtTime(otherMkt, entryUnix)

	// Live mode fallback: query DB if no series loaded
	if volA == 0 && s.db != nil {
		volA = s.queryLatestVolume(marketTicker)
	}
	if volB == 0 && s.db != nil {
		volB = s.queryLatestVolume(otherMkt)
	}

	if volA <= 0 || volB <= 0 {
		return
	}

	// Determine heavy side
	heavyMkt := marketTicker
	heavyPrice := price
	heavyVol := volA
	lightVol := volB
	if volB > volA {
		heavyMkt = otherMkt
		heavyPrice = otherPrice
		heavyVol = volB
		lightVol = volA
	}

	ratio := heavyVol / lightVol
	if ratio < s.cfg.MinVolumeRatio {
		return
	}

	if heavyPrice < s.cfg.MinPrice || heavyPrice > s.cfg.MaxPrice {
		return
	}

	// convProb: heavy money side wins more than price implies.
	// Use price + edge from volume ratio as convProb.
	// Simple model: edge = (ratio - 1) * 5c, capped at 15c.
	edgeCents := int((ratio-1.0)*5.0 + 1e-9)
	if edgeCents > 15 {
		edgeCents = 15
	}
	if edgeCents < 1 {
		return
	}
	convProb := heavyPrice + float64(edgeCents)/100.0
	if convProb > 0.99 {
		convProb = 0.99
	}

	// Re-check edge after clamping
	actualEdge := int((convProb-heavyPrice)*100 + 1e-9)
	if actualEdge < 1 {
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"window_s":    s.cfg.WindowSeconds,
		"close_ts":    closeTs,
		"entry_ts":    entryUnix,
		"heavy_mkt":   heavyMkt,
		"heavy_price": heavyPrice,
		"heavy_vol":   heavyVol,
		"light_vol":   lightVol,
		"ratio":       ratio,
		"conv_prob":   convProb,
	})

	o := store.Order{
		TS:            ts.UnixMilli(),
		MatchTicker:   eventTicker,
		MarketTicker:  heavyMkt,
		Action:        "buy",
		Context:       fmt.Sprintf("volratio_%.1fx_T-%ds", ratio, s.cfg.WindowSeconds),
		ConvProb:      convProb,
		MarketPrice:   heavyPrice,
		EdgeCents:     actualEdge,
		SuggestedSize: s.cfg.BaseSize,
		SetNumber:     0,
		Strategy:      s.cfg.Label,
		Payload:       string(payload),
	}

	if !s.emitter.EmitOrder(o) {
		s.log.Warn("volratio: order dropped", "match", eventTicker, "market", heavyMkt)
		return
	}
	s.mu.Lock()
	s.fired[eventTicker] = true
	s.mu.Unlock()
	s.log.Info("volratio: order emitted",
		"match", eventTicker, "market", heavyMkt,
		"price", heavyPrice, "ratio", ratio, "edge_cents", actualEdge)
}

// queryLatestVolume gets the most recent dollar_volume for a market from DB.
func (s *VolumeRatioStrategy) queryLatestVolume(marketTicker string) float64 {
	vol, err := s.db.GetLatestDollarVolume(context.Background(), marketTicker)
	if err != nil || vol <= 0 {
		return 0
	}
	return vol
}

func (s *VolumeRatioStrategy) SetReplayTime(ts time.Time) {
	s.mu.Lock()
	if ts.IsZero() {
		s.replayNow = nil
	} else {
		t := ts
		s.replayNow = &t
	}
	s.mu.Unlock()
}

func (s *VolumeRatioStrategy) now() time.Time {
	if s.replayNow != nil {
		return *s.replayNow
	}
	return time.Now()
}

func (s *VolumeRatioStrategy) DeletePrice(marketTicker string) {
	s.mu.Lock()
	delete(s.prices, marketTicker)
	delete(s.volumes, marketTicker)
	delete(s.volSeries, marketTicker)
	s.mu.Unlock()
}

func (s *VolumeRatioStrategy) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("VolumeRatioStrategy{%s: markets=%d, fired=%d}",
		s.cfg.Label, len(s.markets), len(s.fired))
}
