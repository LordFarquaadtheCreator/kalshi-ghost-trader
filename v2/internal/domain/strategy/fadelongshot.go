package strategy

import (
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

// FadeLongshot buys the favorite (higher-priced YES) at a fixed time before
// market close. Ported from v1 internal/algorithms/fadelongshot.go —
// decision logic preserved, mutexes removed, EmitOrder replaced with
// returned intents, float convProb replaced with ConvProbBps.
//
// close_ts is proxied by MatchView.OccurrenceTS (match-winner markets
// close at match start). Dynamic convProb is derived from live score
// context tracked via PointScored events.

const (
	flWindowSeconds    = 900
	flMinPriceCents    = 50
	flMinEdgeCents     = 1
	flFixedConvProbBps = 9900 // 0.99
	flBaseConvProbBps  = 9000 // 0.90
	flSetLeadBps       = 300  // +3c per set lead
	flGameLeadBps      = 100  // +1c per game lead
	flMatchPointBps    = 9950 // 0.995
	flSetPointBps      = 9700 // 0.97
	flMaxConvProbBps   = 9990 // 0.999
)

type fadeLongshotState struct {
	fired        bool
	homeSetWins  int
	awaySetWins  int
	homeGames    int
	awayGames    int
	isMatchPoint bool
	isSetPoint   bool
}

// FadeLongshot buys the favorite in the final window before close.
type FadeLongshot struct {
	WindowSeconds   int
	MinPriceCents   int
	MaxPriceCents   int // 0 = no cap
	DynamicConvProb bool
}

func NewFadeLongshot() *FadeLongshot {
	return &FadeLongshot{
		WindowSeconds:   flWindowSeconds,
		MinPriceCents:   flMinPriceCents,
		DynamicConvProb: true,
	}
}

func (s *FadeLongshot) Name() string { return "fadelongshot" }

func (s *FadeLongshot) OnEvent(ev match.Event, st *State) []match.Intent {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return s.onPrice(e, st)
	case match.PointScored:
		return s.onPoint(e, st)
	case match.LifecycleChange:
		if e.Type == "settled" || e.Type == "determined" {
			st.Set("fadelongshot", nil)
		}
	}
	return nil
}

func (s *FadeLongshot) getOrCreateState(st *State) *fadeLongshotState {
	v := st.Get("fadelongshot")
	if v != nil {
		return v.(*fadeLongshotState)
	}
	fs := &fadeLongshotState{}
	st.Set("fadelongshot", fs)
	return fs
}

func (s *FadeLongshot) onPoint(e match.PointScored, st *State) []match.Intent {
	fs := s.getOrCreateState(st)
	p := e.Point
	fs.homeSetWins = p.HomeSetGames
	fs.awaySetWins = p.AwaySetGames
	fs.homeGames = p.HomeGames
	fs.awayGames = p.AwayGames
	fs.isMatchPoint = p.IsMatchPoint
	fs.isSetPoint = p.IsSetPoint
	return s.checkEntry(st, fs, e.TS)
}

func (s *FadeLongshot) onPrice(e match.PriceUpdate, st *State) []match.Intent {
	fs := s.getOrCreateState(st)
	return s.checkEntry(st, fs, e.TS)
}

func (s *FadeLongshot) checkEntry(st *State, fs *fadeLongshotState, now int64) []match.Intent {
	if fs.fired {
		return nil
	}
	mv := &st.MatchView
	if len(mv.MarketTickers) < 2 {
		return nil
	}
	if mv.OccurrenceTS == 0 {
		return nil
	}
	windowMs := int64(s.WindowSeconds) * 1000
	entryTs := mv.OccurrenceTS - windowMs
	if now < entryTs {
		return nil
	}

	p0, ok0 := mv.Prices[mv.MarketTickers[0]]
	p1, ok1 := mv.Prices[mv.MarketTickers[1]]
	if !ok0 || !ok1 {
		return nil
	}
	favMkt := mv.MarketTickers[0]
	favPrice := p0
	if p1 > p0 {
		favMkt = mv.MarketTickers[1]
		favPrice = p1
	}
	if favPrice < s.MinPriceCents {
		return nil
	}
	if s.MaxPriceCents > 0 && favPrice > s.MaxPriceCents {
		return nil
	}

	convProbBps := flFixedConvProbBps
	if s.DynamicConvProb {
		convProbBps = s.dynamicConvProbBps(fs, favPrice)
	}
	edgeCents := convProbBps/100 - favPrice
	if edgeCents < flMinEdgeCents {
		return nil
	}

	fs.fired = true
	return []match.Intent{{
		MarketTicker: favMkt,
		Strategy:     "fadelongshot",
		Action:       "buy",
		PriceCents:   favPrice,
		ConvProbBps:  convProbBps,
		Reason:       "fade_longshot_T-" + intToStr(s.WindowSeconds) + "s",
	}}
}

// dynamicConvProbBps estimates conversion probability from live score context.
// Higher when favorite has set/game lead or is at match/set point.
func (s *FadeLongshot) dynamicConvProbBps(fs *fadeLongshotState, favPriceCents int) int {
	prob := flBaseConvProbBps
	setLead := fs.homeSetWins - fs.awaySetWins
	if setLead < 0 {
		setLead = -setLead
	}
	prob += setLead * flSetLeadBps
	gameLead := fs.homeGames - fs.awayGames
	if gameLead < 0 {
		gameLead = -gameLead
	}
	prob += gameLead * flGameLeadBps
	if fs.isMatchPoint {
		prob = flMatchPointBps
	}
	if fs.isSetPoint && !fs.isMatchPoint && prob < flSetPointBps {
		prob = flSetPointBps
	}
	// must stay above favPrice to have edge
	favBps := favPriceCents * 100
	if prob <= favBps {
		prob = favBps + 100 // +1 cent in bps
	}
	if prob > flMaxConvProbBps {
		prob = flMaxConvProbBps
	}
	return prob
}
