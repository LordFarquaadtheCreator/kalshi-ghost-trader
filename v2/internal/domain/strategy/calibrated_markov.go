package strategy

import (
	"encoding/json"
	"math"
	"os"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/pricing"
)

// CalibratedMarkov uses ML-calibrated serve-win probability to compute
// Markov fair value. Trades when market diverges from calibrated fair
// value by more than threshold. Fires once per match.
//
// Ported from v1 internal/algorithms/calibrated_markov.go — decision logic
// preserved, mutexes removed, EmitOrder replaced with returned intents,
// prices in cents, ConvProb in basis points.

const (
	cmMinEdgeCents  = 3
	cmMaxPriceCents = 85
	cmDefaultPServe = 0.64
)

// serveWinModel holds logistic regression weights loaded from JSON.
// Trained offline by research/ml/train_serve_win.py.
type serveWinModel struct {
	Coef        []float64      `json:"coef"`
	Intercept   float64        `json:"intercept"`
	SeriesMap   map[string]int `json:"series_map"`
	PointMap    map[string]int `json:"point_map"`
	OverallRate float64        `json:"overall_rate"`
}

type cmState struct {
	fired  bool
	series string
}

// CalibratedMarkov computes fair value from calibrated serve-win probability.
type CalibratedMarkov struct {
	modelPath string
	model     *serveWinModel
}

// NewCalibratedMarkov loads the serve-win model from modelPath. If the path
// is empty or loading fails, falls back to default pServe=0.64.
func NewCalibratedMarkov(modelPath string) *CalibratedMarkov {
	s := &CalibratedMarkov{modelPath: modelPath}
	s.loadModel()
	return s
}

func (s *CalibratedMarkov) loadModel() {
	if s.modelPath == "" {
		return
	}
	data, err := os.ReadFile(s.modelPath)
	if err != nil {
		return
	}
	var m serveWinModel
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	s.model = &m
}

func (s *CalibratedMarkov) Name() string { return "calibrated-markov" }

func (s *CalibratedMarkov) OnEvent(ev match.Event, st *State) []match.Intent {
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

func (s *CalibratedMarkov) onPoint(e match.PointScored, st *State) []match.Intent {
	mv := &st.MatchView
	cs := s.getOrCreateState(st)
	if cs.fired {
		return nil
	}
	if len(mv.MarketTickers) < 2 {
		return nil
	}

	homeMkt, awayMkt := mv.MarketTickers[0], mv.MarketTickers[1]
	homePrice, homeOK := mv.Prices[homeMkt]
	awayPrice, awayOK := mv.Prices[awayMkt]
	if (!homeOK || homePrice <= 0) && (!awayOK || awayPrice <= 0) {
		return nil
	}

	// Calibrated pServe for this context.
	pServe := s.predictServeWin(cs.series, e.Point)
	mk := pricing.NewMarkovModelWithProb(pServe)

	// Sets won from set games.
	setsHome, setsAway := 0, 0
	if e.Point.HomeSetGames > e.Point.AwaySetGames {
		setsHome = 1
	} else if e.Point.AwaySetGames > e.Point.HomeSetGames {
		setsAway = 1
	}

	homeFV := mk.WinProbability(
		setsHome, setsAway, e.Point.HomeGames, e.Point.AwayGames,
		e.Point.HomePoints, e.Point.AwayPoints, e.Point.Server, e.Point.IsTiebreak,
	)
	awayFV := 1.0 - homeFV

	var intents []match.Intent
	if homeOK && homePrice > 0 {
		if in := s.checkEdge(homeMkt, homePrice, homeFV, e.Point.SetNumber, cs); in != nil {
			intents = append(intents, *in)
		}
	}
	// v1 fires once per match — only check away if home didn't fire.
	if !cs.fired && awayOK && awayPrice > 0 {
		if in := s.checkEdge(awayMkt, awayPrice, awayFV, e.Point.SetNumber, cs); in != nil {
			intents = append(intents, *in)
		}
	}
	return intents
}

func (s *CalibratedMarkov) checkEdge(mkt string, priceCents int, fairValue float64, setNum int, cs *cmState) *match.Intent {
	if priceCents <= 0 || priceCents > cmMaxPriceCents {
		return nil
	}
	fvCents := int(fairValue * 100)
	edgeCents := fvCents - priceCents
	if edgeCents < cmMinEdgeCents {
		return nil
	}
	cs.fired = true
	return &match.Intent{
		MarketTicker: mkt,
		Strategy:     "calibrated-markov",
		Action:       "buy",
		PriceCents:   priceCents,
		ConvProbBps:  int(fairValue * 10000),
		Reason:       "calmarkov_set" + intToStr(setNum) + "_edge" + intToStr(edgeCents) + "c",
	}
}

// predictServeWin computes P(server wins point | context) using logistic model.
// Falls back to default pServe if no model loaded.
func (s *CalibratedMarkov) predictServeWin(seriesTicker string, p match.Point) float64 {
	if s.model == nil {
		return cmDefaultPServe
	}
	seriesID, ok := s.model.SeriesMap[seriesTicker]
	if !ok {
		seriesID = -1
	}
	hp := s.model.PointMap[p.HomePoints]
	if hp == 0 && p.HomePoints != "0" {
		hp = 4 // A
	}
	ap := s.model.PointMap[p.AwayPoints]
	if ap == 0 && p.AwayPoints != "0" {
		ap = 4
	}
	serverIsHome := 0
	if p.Server == 1 {
		serverIsHome = 1
	}
	isBPInt := 0
	if p.IsBreakPoint {
		isBPInt = 1
	}
	isTBInt := 0
	if p.IsTiebreak {
		isTBInt = 1
	}
	pointDiff := hp - ap
	gameDiff := p.HomeGames - p.AwayGames

	// Feature order must match training:
	// series_id, server, home_games, away_games, point_diff, game_diff,
	// is_bp, is_tb, server_is_home, hp, ap
	features := []float64{
		float64(seriesID), float64(p.Server), float64(p.HomeGames), float64(p.AwayGames),
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
	return cmSigmoid(z)
}

func cmSigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

func (s *CalibratedMarkov) getOrCreateState(st *State) *cmState {
	v := st.Get("calibrated-markov")
	if v != nil {
		return v.(*cmState)
	}
	cs := &cmState{}
	st.Set("calibrated-markov", cs)
	return cs
}

func (s *CalibratedMarkov) cleanup(st *State) {
	st.Set("calibrated-markov", nil)
}
