// Package signal — close_timer.go
//
// Close timer strategy: buy the favorite N minutes before market close.
//
// Empirical backtest (research/strategy_analysis/nothing_happens.py):
//   - T-10min, favorite ≥85c: n=42, 100% hit, Sharpe 1.01
//   - T-10min, favorite ≥70c: n=59, 100% hit, Sharpe 0.97
//
// The market under-prices near-certainty. A favorite at 85c+ with 10min
// left almost always wins. We buy their YES contract, hold to settlement.
//
// Flow:
//   1. Poll DB for active markets closing within leadMinutes
//   2. For each event, look up both markets' live prices from the Generator
//   3. Pick the higher-priced side (the favorite)
//   4. If favorite price ≥ minPrice, emit a simulated buy order
//   5. Dedup per event — one order per event per close window

package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

const priceStaleTTL = 60 * time.Second

// PriceLookup returns the current YES price for a market ticker.
// Implemented by algorithms.MatchPointStrategy.
type PriceLookup = algorithms.PriceLookup

// CloseTimer watches for markets approaching close and fires buy orders
// on the favorite when its price exceeds the threshold.
type CloseTimer struct {
	db         *store.DB
	prices     PriceLookup
	tickWriter *store.TickWriter
	leadMin    int
	minPrice   float64
	size       float64
	log        *slog.Logger

	mu    sync.Mutex
	fired map[string]bool // event_ticker -> order already emitted
}

// NewCloseTimer creates a close-timer strategy instance.
func NewCloseTimer(db *store.DB, prices PriceLookup, tw *store.TickWriter,
	leadMin int, minPrice, size float64, log *slog.Logger) *CloseTimer {
	return &CloseTimer{
		db:         db,
		prices:     prices,
		tickWriter: tw,
		leadMin:    leadMin,
		minPrice:   minPrice,
		size:       size,
		log:        log,
		fired:      make(map[string]bool),
	}
}

// Run polls the DB for markets approaching close and fires orders.
func (ct *CloseTimer) Run(ctx context.Context, pollSecs int) error {
	interval := time.Duration(pollSecs) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ct.scan(ctx, pollSecs)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ct.scan(ctx, pollSecs)
		}
	}
}

func (ct *CloseTimer) scan(ctx context.Context, pollSecs int) {
	// look ahead leadMin + buffer for the query, but only fire when within leadMin
	lookahead := int64(ct.leadMin*60 + max(pollSecs, 60))
	markets, err := ct.db.GetMarketsClosingWithin(ctx, lookahead)
	if err != nil {
		ct.log.Error("close timer: query markets", "err", err)
		return
	}

	// group markets by event — two markets per event (one per player)
	byEvent := make(map[string][]store.Market)
	for _, m := range markets {
		byEvent[m.EventTicker] = append(byEvent[m.EventTicker], m)
	}

	now := time.Now()
	firedThisCycle := 0

	for eventTicker, mkts := range byEvent {
		// dedup — one order per event
		ct.mu.Lock()
		already := ct.fired[eventTicker]
		ct.mu.Unlock()
		if already {
			continue
		}

		// check timing — only fire when within leadMin of close
		var closeTS int64
		for _, m := range mkts {
			if m.CloseTS > closeTS {
				closeTS = m.CloseTS
			}
		}
		if closeTS == 0 {
			continue
		}
		closeTime := time.UnixMilli(closeTS)
		secsToClose := closeTime.Sub(now).Seconds()
		if secsToClose > float64(ct.leadMin*60) {
			continue
		}
		if secsToClose < 0 {
			// past close time — skip without marking as fired.
			// if Kalshi extends close_ts, we want to re-evaluate.
			continue
		}
		// backtest excluded final 60s — markets often illiquid/halted then
		if secsToClose < 60 {
			continue
		}

		// need both markets to identify the favorite — one side alone
		// could be the underdog if the other already settled.
		if len(mkts) < 2 {
			ct.log.Debug("close timer: fewer than 2 markets for event, skipping",
				"event", eventTicker, "count", len(mkts))
			continue
		}

		// find the favorite — higher-priced side with a fresh price
		var favMkt store.Market
		var favPrice float64
		for _, m := range mkts {
			p := ct.prices.GetPrice(m.MarketTicker)
			if p <= 0 {
				continue
			}
			if ct.prices.GetPriceAge(m.MarketTicker) > priceStaleTTL {
				continue
			}
			if p > favPrice {
				favPrice = p
				favMkt = m
			}
		}

		// no price at all — WS not subscribed or no ticks yet
		if favPrice <= 0 {
			ct.log.Debug("close timer: no price for either market, skipping",
				"event", eventTicker)
			continue
		}

		if favPrice < ct.minPrice {
			ct.log.Debug("close timer: favorite below threshold",
				"event", eventTicker, "price", favPrice, "threshold", ct.minPrice)
			continue
		}

		// Conservative conversion prob for logging — backtest showed 100%
		// but small sample. Real rate likely 95-97%.
		const favConvProb = 0.95
		// Edge = profit to settlement per share (buy at favPrice, settle at $1).
		// Entry criterion is minPrice (backtest tested ≥85c), not edge-vs-model.
		edgeCents := int((1.0 - favPrice) * 100)
		payload, _ := json.Marshal(map[string]any{
			"strategy":       "close_timer",
			"secs_to_close":  int(secsToClose),
			"favorite":       favMkt.PlayerName,
			"favorite_price": favPrice,
		})

		o := store.Order{
			TS:            now.UnixMilli(),
			MatchTicker:   eventTicker,
			MarketTicker:  favMkt.MarketTicker,
			Action:        "buy",
			Context:       fmt.Sprintf("close_timer_%dm", ct.leadMin),
			ConvProb:      favConvProb,
			MarketPrice:   favPrice,
			EdgeCents:     edgeCents,
			SuggestedSize: ct.size,
			SetNumber:     0,
			Strategy:      "close_timer",
			Payload:       string(payload),
		}

		if !ct.tickWriter.IngestOrder(o) {
			ct.log.Warn("close timer: order dropped, not marking fired", "event", eventTicker)
			continue
		}
		ct.mu.Lock()
		ct.fired[eventTicker] = true
		ct.mu.Unlock()

		ct.log.Info("close timer: order emitted",
			"event", eventTicker, "market", favMkt.MarketTicker,
			"player", favMkt.PlayerName, "price", favPrice,
			"edge_cents", edgeCents, "secs_to_close", int(secsToClose),
			"size", ct.size)
		firedThisCycle++
	}

	// cleanup: evict fired entries for events no longer in the closing window
	// (settled/closed/finalized). keeps the map from growing unbounded.
	ct.cleanupFired(byEvent)

	if firedThisCycle > 0 {
		ct.log.Info("close timer scan complete", "fired", firedThisCycle,
			"events_closing", len(byEvent))
	}
}

// cleanupFired removes entries for events that are no longer active
// (not in the current closing set — they've settled or closed).
func (ct *CloseTimer) cleanupFired(current map[string][]store.Market) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	for evt := range ct.fired {
		if _, ok := current[evt]; !ok {
			delete(ct.fired, evt)
		}
	}
}
