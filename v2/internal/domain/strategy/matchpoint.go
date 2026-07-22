package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// matchpointState tracks per-event set tracking and point dedup.
type matchpointState struct {
	setsHome      int
	setsAway      int
	lastSetNum    int
	lastHomeGames int
	lastAwayGames int
	lastScorer    int
	seenPoints    map[string]bool
}

const (
	mpServeConvProb = 0.97
	mpSetsToWin     = 2
	mpGamesPerSet   = 6
	mpMinEdgeCents  = 1
	mpMinPriceCents = 5 // 0.05
	mpPriceStaleMS  = 60_000
)

// MatchPoint emits buy intents when a player reaches match point while serving.
// Ported from v1 internal/algorithms/matchpoint.go — decision logic preserved
// verbatim, mutexes removed, EmitOrder replaced with returned intents.
type MatchPoint struct{}

func NewMatchPoint() *MatchPoint { return &MatchPoint{} }

func (s *MatchPoint) Name() string { return "matchpoint" }

func (s *MatchPoint) OnEvent(ev match.Event, st *State) []match.Intent {
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

func (s *MatchPoint) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	ms := s.getOrCreateState(st)

	s.updateMatchState(ms, e.Point)

	pointKey := pointKeyStr(e.Point.SetNumber, e.Point.GameNumber, e.Point.PointNumber)
	if ms.seenPoints[pointKey] {
		return nil
	}
	ms.seenPoints[pointKey] = true

	mp := s.detectMatchPoint(ms, e.Point)
	if mp == nil {
		return nil
	}

	isServing := (mp.winner == 1 && e.Point.Server == 1) || (mp.winner == 2 && e.Point.Server == 2)
	if !isServing {
		return nil
	}

	if len(mv.MarketTickers) < 2 {
		return nil
	}

	var marketTicker string
	if mp.winner == 1 {
		marketTicker = mv.MarketTickers[0]
	} else {
		marketTicker = mv.MarketTickers[1]
	}

	priceCents, ok := mv.Prices[marketTicker]
	if !ok || priceCents < mpMinPriceCents {
		return nil
	}

	priceTS, hasTS := mv.PriceTS[marketTicker]
	if !hasTS {
		return nil
	}
	if e.TS-priceTS > mpPriceStaleMS {
		return nil
	}

	convProbBps := int(mpServeConvProb * 10000)
	edgeCents := convProbBps/100 - priceCents
	if edgeCents < mpMinEdgeCents {
		return nil
	}

	return []match.Intent{{
		MarketTicker: marketTicker,
		Strategy:     "matchpoint",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  convProbBps,
		Reason:       mp.context,
	}}
}

func (s *MatchPoint) getOrCreateState(st *State) *matchpointState {
	v := st.Get("matchpoint")
	if v != nil {
		return v.(*matchpointState)
	}
	ms := &matchpointState{seenPoints: make(map[string]bool)}
	st.Set("matchpoint", ms)
	return ms
}

func (s *MatchPoint) cleanup(st *State) {
	st.Set("matchpoint", nil)
}

func (s *MatchPoint) updateMatchState(ms *matchpointState, p match.Point) {
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

type matchPointDetected struct {
	winner  int
	context string
}

func (s *MatchPoint) detectMatchPoint(ms *matchpointState, p match.Point) *matchPointDetected {
	setsHome, setsAway := ms.setsHome, ms.setsAway
	gamesHome, gamesAway := p.HomeGames, p.AwayGames

	homeNeedsSet := mpSetsToWin - setsHome
	awayNeedsSet := mpSetsToWin - setsAway
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

	var homeMatchPoint, awayMatchPoint bool
	if homeOneSetAway && homeCanWinGame && gamesHome >= mpGamesPerSet-1 && gamesHome > gamesAway {
		homeMatchPoint = true
	}
	if awayOneSetAway && awayCanWinGame && gamesAway >= mpGamesPerSet-1 && gamesAway > gamesHome {
		awayMatchPoint = true
	}

	if !homeMatchPoint && !awayMatchPoint {
		return nil
	}

	winner := 2
	ctx := "away_match_point"
	if homeMatchPoint {
		winner = 1
		ctx = "home_match_point"
	}

	return &matchPointDetected{winner: winner, context: ctx}
}

// --- shared helpers moved to helpers.go ---
