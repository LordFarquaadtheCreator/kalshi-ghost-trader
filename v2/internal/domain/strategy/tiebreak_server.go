package strategy

import (
	"fmt"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// TiebreakServer buys the server's YES when a tiebreak starts.
// Empirical: server wins 60% of TB points. Markov assumes 50/50.
// Edge = (0.60 - market_price) * 100. Fires once per tiebreak per match.
//
// Ported from v1 internal/algorithms/tiebreak_server.go — decision
// logic preserved, mutexes removed, EmitOrder replaced with returned
// intents.
type TiebreakServer struct {
	// MinEdgeCents: minimum edge to fire (default 5).
	MinEdgeCents int
	// MaxMarketPriceCents: max price to buy at (default 65).
	MaxMarketPriceCents int
	// ConvProbBps: estimated server win prob in tiebreak (default 6000 = 0.60).
	ConvProbBps int
}

func NewTiebreakServer() *TiebreakServer {
	return &TiebreakServer{
		MinEdgeCents:       5,
		MaxMarketPriceCents: 65,
		ConvProbBps:        6000,
	}
}

func (s *TiebreakServer) Name() string { return "tiebreak-server" }

// tiebreakServerState holds per-match mutable state.
type tiebreakServerState struct {
	fired bool
}

func (s *TiebreakServer) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("tiebreak-server", nil)
		}
	}
	return nil
}

func (s *TiebreakServer) getOrCreateState(st *State) *tiebreakServerState {
	if v := st.Get("tiebreak-server"); v != nil {
		return v.(*tiebreakServerState)
	}
	ss := &tiebreakServerState{}
	st.Set("tiebreak-server", ss)
	return ss
}

// onPoint detects tiebreak start (is_tiebreak=true, point 1) and fires.
func (s *TiebreakServer) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}
	// Fire on first tiebreak point.
	if !e.Point.IsTiebreak || e.Point.PointNumber != 1 {
		return nil
	}
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	// Server 1=home → market[0], Server 2=away → market[1].
	serverMkt := mv.MarketTickers[0]
	if e.Point.Server == 2 {
		serverMkt = mv.MarketTickers[1]
	}

	price, ok := mv.Prices[serverMkt]
	if !ok || price <= 0 || price > s.MaxMarketPriceCents {
		return nil
	}

	convProbCents := s.ConvProbBps / 100
	edgeCents := convProbCents - price
	if edgeCents < s.MinEdgeCents {
		return nil
	}

	ss.fired = true
	return []match.Intent{{
		MarketTicker: serverMkt,
		Strategy:     "tiebreak-server",
		Action:       "buy",
		PriceCents:   price,
		ConvProbBps:  s.ConvProbBps,
		Reason:       fmt.Sprintf("tbserver_set%d", e.Point.SetNumber),
	}}
}
