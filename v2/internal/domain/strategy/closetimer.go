package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// closeTimerState tracks whether an order already fired for this close window.
// Dedup is per-event — one order per event per close window.
type closeTimerState struct {
	fired bool
}

const (
	ctLeadMin       = 10              // fire within 10 min of close
	ctMinPriceCents = 85              // favorite must be >= 85c
	ctPriceStaleMS  = 60_000          // price older than 60s = no order
	ctFinalWindowS  = 60              // skip final 60s — illiquid/halted
	ctConvProbBps   = 9500            // 0.95 — conservative vs 100% backtest
)

// CloseTimer buys the favorite N minutes before market close.
// Ported from v1 internal/signal/close_timer.go — timer-driven OnTick becomes
// ClockTick-driven OnEvent. Mutexes removed (single-threaded loop), EmitOrder
// replaced with returned intents. DB polling removed (per-match event model).
//
// Empirical backtest: T-10min, favorite >=85c won 100% (n=42, Sharpe 1.01).
// The market under-prices near-certainty; we buy the favorite's YES contract
// and hold to settlement.
type CloseTimer struct{}

func NewCloseTimer() *CloseTimer { return &CloseTimer{} }

func (s *CloseTimer) Name() string { return "close_timer" }

func (s *CloseTimer) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.ClockTick:
		return s.onTick(e, st)
	case match.LifecycleChange:
		// reset on settlement so a close_ts extension re-evaluates
		if e.Type == "settled" || e.Type == "determined" {
			s.cleanup(st)
		}
	}
	return nil
}

func (s *CloseTimer) onTick(e match.ClockTick, st *State) []match.Intent {
	mv := &st.MatchView

	// need close time and both markets to identify the favorite
	if mv.CloseTS == 0 || len(mv.MarketTickers) < 2 {
		return nil
	}

	now := e.TS
	secsToClose := (mv.CloseTS - now) / 1000

	// only fire within leadMin of close
	if secsToClose > int64(ctLeadMin*60) {
		return nil
	}
	// past close — skip without marking fired; close_ts may extend
	if secsToClose < 0 {
		return nil
	}
	// final 60s excluded — markets often illiquid/halted
	if secsToClose < int64(ctFinalWindowS) {
		return nil
	}

	cs := s.getOrCreateState(st)
	if cs.fired {
		return nil
	}

	homeTicker := mv.MarketTickers[0]
	awayTicker := mv.MarketTickers[1]

	homePrice, homeOK := mv.Prices[homeTicker]
	awayPrice, awayOK := mv.Prices[awayTicker]

	// both sides need a fresh price — one side alone could be the underdog
	// if the other already settled
	if !homeOK || !awayOK {
		return nil
	}
	if homePrice <= 0 || awayPrice <= 0 {
		return nil
	}

	homeTS, homeTSOK := mv.PriceTS[homeTicker]
	awayTS, awayTSOK := mv.PriceTS[awayTicker]
	if !homeTSOK || !awayTSOK {
		return nil
	}
	if now-homeTS > ctPriceStaleMS || now-awayTS > ctPriceStaleMS {
		return nil
	}

	// pick the favorite — higher-priced side
	var favTicker string
	var favPrice int
	if homePrice >= awayPrice {
		favTicker = homeTicker
		favPrice = homePrice
	} else {
		favTicker = awayTicker
		favPrice = awayPrice
	}

	if favPrice < ctMinPriceCents {
		return nil
	}

	cs.fired = true

	return []match.Intent{{
		MarketTicker: favTicker,
		Strategy:     "close_timer",
		Action:       "buy",
		PriceCents:   favPrice,
		ConvProbBps:  ctConvProbBps,
		Reason:       "close_timer_" + intToStr(ctLeadMin) + "m",
	}}
}

func (s *CloseTimer) getOrCreateState(st *State) *closeTimerState {
	v := st.Get("close_timer")
	if v != nil {
		return v.(*closeTimerState)
	}
	cs := &closeTimerState{}
	st.Set("close_timer", cs)
	return cs
}

func (s *CloseTimer) cleanup(st *State) {
	st.Set("close_timer", nil)
}
