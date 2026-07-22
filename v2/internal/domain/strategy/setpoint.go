package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// setpointState tracks per-event set tracking and point dedup.
type setpointState struct {
	setsHome      int
	setsAway      int
	lastSetNum    int
	lastHomeGames int
	lastAwayGames int
	lastScorer    int
	seenPoints    map[string]bool
}

const (
	spSetsToWin         = 2
	spGamesPerSet       = 6
	spMinEdgeCents      = 1
	spMinPriceCents     = 5 // 0.05
	spPriceStaleMS      = 60_000
	spServeConvProbBps  = 9300 // 0.93
	spReturnConvProbBps = 8900 // 0.89
	spIncludeSetPoints  = true
	spIncludeReturning  = true
)

// SetPoint emits buy intents when a player reaches set point (serving or
// returning). Generalizes MatchPoint to fire on any set point, not just
// match-deciding ones. Data shows set points convert at 91% (93% serving,
// 89% returning) but markets price them at 56c avg — a large edge.
// Ported from v1 internal/algorithms/setpoint.go — decision logic preserved,
// mutexes removed, EmitOrder replaced with returned intents.
type SetPoint struct{}

func NewSetPoint() *SetPoint { return &SetPoint{} }

func (s *SetPoint) Name() string { return "setpoint" }

func (s *SetPoint) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			s.cleanup(st)
		}
	}
	return nil
}

func (s *SetPoint) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	ms := s.getOrCreateState(st)

	s.updateMatchState(ms, e.Point)

	pointKey := pointKeyStr(e.Point.SetNumber, e.Point.GameNumber, e.Point.PointNumber)
	if ms.seenPoints[pointKey] {
		return nil
	}
	ms.seenPoints[pointKey] = true

	sp := s.detectSetPoint(ms, e.Point)
	if sp == nil {
		return nil
	}

	isServing := (sp.winner == 1 && e.Point.Server == 1) || (sp.winner == 2 && e.Point.Server == 2)
	if !isServing && !spIncludeReturning {
		return nil
	}

	if len(mv.MarketTickers) < 2 {
		return nil
	}

	var marketTicker string
	if sp.winner == 1 {
		marketTicker = mv.MarketTickers[0]
	} else {
		marketTicker = mv.MarketTickers[1]
	}

	priceCents, ok := mv.Prices[marketTicker]
	if !ok || priceCents < spMinPriceCents {
		return nil
	}

	priceTS, hasTS := mv.PriceTS[marketTicker]
	if !hasTS {
		return nil
	}
	if e.TS-priceTS > spPriceStaleMS {
		return nil
	}

	convProbBps := spServeConvProbBps
	if !isServing {
		convProbBps = spReturnConvProbBps
	}

	edgeCents := convProbBps/100 - priceCents
	if edgeCents < spMinEdgeCents {
		return nil
	}

	return []match.Intent{{
		MarketTicker: marketTicker,
		Strategy:     "setpoint",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  convProbBps,
		Reason:       sp.context,
	}}
}

func (s *SetPoint) getOrCreateState(st *State) *setpointState {
	v := st.Get("setpoint")
	if v != nil {
		return v.(*setpointState)
	}
	ms := &setpointState{seenPoints: make(map[string]bool)}
	st.Set("setpoint", ms)
	return ms
}

func (s *SetPoint) cleanup(st *State) {
	st.Set("setpoint", nil)
}

func (s *SetPoint) updateMatchState(ms *setpointState, p match.Point) {
	if p.SetNumber > ms.lastSetNum && ms.lastSetNum > 0 {
		if ms.lastHomeGames > ms.lastAwayGames {
			ms.setsHome++
		} else if ms.lastAwayGames > ms.lastHomeGames {
			ms.setsAway++
		} else if ms.lastScorer != 0 {
			if ms.lastScorer == 1 {
				ms.setsHome++
			} else {
				ms.setsAway++
			}
		}
	}
	ms.lastSetNum = p.SetNumber
	ms.lastHomeGames = p.HomeGames
	ms.lastAwayGames = p.AwayGames
	ms.lastScorer = p.Scorer
}

type setPointSignal struct {
	winner       int
	context      string
	isMatchPoint bool
}

func (s *SetPoint) detectSetPoint(ms *setpointState, p match.Point) *setPointSignal {
	setsHome, setsAway := ms.setsHome, ms.setsAway

	homeNeedsSet := spSetsToWin - setsHome
	awayNeedsSet := spSetsToWin - setsAway
	if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
		return nil
	}

	homeOneSetAway := homeNeedsSet == 1
	awayOneSetAway := awayNeedsSet == 1

	if p.IsTiebreak {
		return nil
	}

	homeCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 1)
	awayCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 2)

	homeCanWinSet := homeCanWinGame && p.HomeGames >= spGamesPerSet-1 && p.HomeGames > p.AwayGames
	awayCanWinSet := awayCanWinGame && p.AwayGames >= spGamesPerSet-1 && p.AwayGames > p.HomeGames

	if !homeCanWinSet && !awayCanWinSet {
		return nil
	}

	homeIsMP := homeCanWinSet && homeOneSetAway
	awayIsMP := awayCanWinSet && awayOneSetAway

	if !spIncludeSetPoints && !homeIsMP && !awayIsMP {
		return nil
	}

	winner := 2
	ctx := "away_set_point"
	if homeCanWinSet {
		winner = 1
		ctx = "home_set_point"
	}
	if homeIsMP {
		ctx = "home_match_point"
	} else if awayIsMP {
		ctx = "away_match_point"
	}

	return &setPointSignal{
		winner:       winner,
		context:      ctx,
		isMatchPoint: homeIsMP || awayIsMP,
	}
}
