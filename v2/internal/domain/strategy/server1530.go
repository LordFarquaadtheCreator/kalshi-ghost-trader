package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// Server1530 buys a favourite's YES after a small price dip or when the
// score reaches 15-30 on the server, betting the server holds.
// Ported from v1 internal/algorithms/server1530.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents,
// float convProb replaced with ConvProbBps.
//
// Two trigger paths:
//   - Price dip: prev price drops 3-8% on a favourite (peak >= MinFavCents).
//   - Score: 15-30 on the server's market, peak >= MinFavCents.
//
// Per-market prev/peak prices tracked in strategy state since MatchView
// only exposes the latest price.

const (
	s1530MinFavCents   = 55
	s1530MinDipBps     = 300 // 3%
	s1530MaxDipBps     = 800 // 8%
	s1530MaxEntryCents = 65
	s1530ConvProbBps   = 6200 // 0.62
	s1530MinEdgeCents  = 1
)

type server1530State struct {
	fired bool
	prev  map[string]int // market_ticker -> previous price (cents)
	max   map[string]int // market_ticker -> peak price (cents)
}

// Server1530 buys the server's YES at a 15-30 pressure-point dip.
type Server1530 struct {
	MinFavCents   int
	MinDipBps     int
	MaxDipBps     int
	MaxEntryCents int
	ConvProbBps   int
}

func NewServer1530() *Server1530 {
	return &Server1530{
		MinFavCents:   s1530MinFavCents,
		MinDipBps:     s1530MinDipBps,
		MaxDipBps:     s1530MaxDipBps,
		MaxEntryCents: s1530MaxEntryCents,
		ConvProbBps:   s1530ConvProbBps,
	}
}

func (s *Server1530) Name() string { return "server1530" }

func (s *Server1530) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("server1530", nil)
		}
	}
	return nil
}

func (s *Server1530) getOrCreateState(st *State) *server1530State {
	v := st.Get("server1530")
	if v != nil {
		return v.(*server1530State)
	}
	ss := &server1530State{
		prev: make(map[string]int),
		max:  make(map[string]int),
	}
	st.Set("server1530", ss)
	return ss
}

func (s *Server1530) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	ss := s.getOrCreateState(st)
	mkt := e.MarketTicker
	prev := ss.prev[mkt]
	cur := e.PriceCents
	ss.prev[mkt] = cur
	if cur > ss.max[mkt] {
		ss.max[mkt] = cur
	}
	if ss.fired {
		return nil
	}
	maxPrice := ss.max[mkt]
	if maxPrice < s.MinFavCents {
		return nil
	}
	// need a previous price to detect dip
	if prev <= 0 {
		return nil
	}
	dipBps := (prev - cur) * 10000 / prev
	if dipBps < s.MinDipBps || dipBps > s.MaxDipBps {
		return nil
	}
	if cur > s.MaxEntryCents {
		return nil
	}
	edgeCents := s.ConvProbBps/100 - cur
	if edgeCents < s1530MinEdgeCents {
		return nil
	}
	ss.fired = true
	return []match.Intent{{
		MarketTicker: mkt,
		Strategy:     "server1530",
		Action:       "buy",
		PriceCents:   cur,
		ConvProbBps:  s.ConvProbBps,
		Reason:       "server1530_dip_" + intToStr(dipBps/10) + "pct",
	}}
}

func (s *Server1530) onPoint(e match.PointScored, st *State) []match.Intent {
	ss := s.getOrCreateState(st)
	if ss.fired {
		return nil
	}
	p := e.Point
	homeIs15 := p.HomePoints == "15"
	awayIs30 := p.AwayPoints == "30"
	awayIs15 := p.AwayPoints == "15"
	homeIs30 := p.HomePoints == "30"
	is1530onHome := homeIs15 && awayIs30 && p.Server == 1
	is1530onAway := awayIs15 && homeIs30 && p.Server == 2
	if !is1530onHome && !is1530onAway {
		return nil
	}
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	serverMkt := mv.MarketTickers[0]
	if p.Server == 2 {
		serverMkt = mv.MarketTickers[1]
	}
	maxPrice := ss.max[serverMkt]
	if maxPrice < s.MinFavCents {
		return nil
	}
	price, ok := mv.Prices[serverMkt]
	if !ok || price <= 0 || price > s.MaxEntryCents {
		return nil
	}
	edgeCents := s.ConvProbBps/100 - price
	if edgeCents < s1530MinEdgeCents {
		return nil
	}
	ss.fired = true
	return []match.Intent{{
		MarketTicker: serverMkt,
		Strategy:     "server1530",
		Action:       "buy",
		PriceCents:   price,
		ConvProbBps:  s.ConvProbBps,
		Reason:       "server1530_set" + intToStr(p.SetNumber) + "_game" + intToStr(p.GameNumber),
	}}
}
