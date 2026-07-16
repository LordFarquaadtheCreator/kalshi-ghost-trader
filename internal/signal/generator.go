package signal

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

const (
	minEdgeCents  = 1
	baseSize      = 10.0
	maxSize       = 100.0
	setsToWin     = 2
	gamesPerSet   = 6
	priceStaleTTL = 60 * time.Second

	// Empirical conversion rate: when serving for the match, players convert
	// 97.3% of the time (backtest: research/strategy_analysis/match_point_edge.py).
	// Returning (breaking for match): 88.5% — higher edge but higher variance.
	// We only fire on serve match points — cleaner signal, lower variance.
	serveConvProb = 0.97
)

// matchState tracks per-match set scores derived from point flow.
// FlashScore points arrive sequentially; when SetNumber increments,
// the previous set's final game scores determine who won that set.
type matchState struct {
	setsHome      int
	setsAway      int
	lastSetNum    int
	lastHomeGames int
	lastAwayGames int
	lastScorer    int // 1=home, 2=away — last point scorer (for tiebreak set winner)
}

type Generator struct {
	mu          sync.RWMutex
	prices      map[string]float64         // market_ticker -> latest YES price (0-1)
	priceTimes  map[string]time.Time       // market_ticker -> last price update
	markets     map[string][]string        // event_ticker -> [home_ticker, away_ticker]
	matchStates map[string]*matchState     // event_ticker -> set tracking state
	seenPoints  map[string]map[string]bool // event_ticker -> dedup set ("set:game:point")
	tickWriter  *store.TickWriter
	log         *slog.Logger
}

func New(tw *store.TickWriter, log *slog.Logger) *Generator {
	return &Generator{
		prices:      make(map[string]float64),
		priceTimes:  make(map[string]time.Time),
		markets:     make(map[string][]string),
		matchStates: make(map[string]*matchState),
		seenPoints:  make(map[string]map[string]bool),
		tickWriter:  tw,
		log:         log,
	}
}

func (g *Generator) RegisterMarkets(eventTicker string, marketTickers []string) {
	g.mu.Lock()
	g.markets[eventTicker] = marketTickers
	g.mu.Unlock()
}

// UnregisterMarkets removes all state for a match — markets, set tracking,
// dedup cache, and prices for the associated market tickers.
func (g *Generator) UnregisterMarkets(eventTicker string) {
	g.mu.Lock()
	// Clean prices for associated market tickers before removing mapping
	for _, mkt := range g.markets[eventTicker] {
		delete(g.prices, mkt)
		delete(g.priceTimes, mkt)
	}
	delete(g.markets, eventTicker)
	delete(g.matchStates, eventTicker)
	delete(g.seenPoints, eventTicker)
	g.mu.Unlock()
}

func (g *Generator) UpdatePrice(marketTicker string, price float64) {
	g.mu.Lock()
	g.prices[marketTicker] = price
	g.priceTimes[marketTicker] = time.Now()
	g.mu.Unlock()
}

func (g *Generator) OnPoints(pts []store.Point) {
	for _, p := range pts {
		g.updateMatchState(p)
		g.processPoint(p)
	}
}

// updateMatchState tracks set scores by detecting set transitions.
// When SetNumber increments, the previous set's final game scores
// (from the last point seen) determine the set winner.
func (g *Generator) updateMatchState(p store.Point) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms, ok := g.matchStates[p.MatchTicker]
	if !ok {
		ms = &matchState{}
		g.matchStates[p.MatchTicker] = ms
	}
	if p.SetNumber > ms.lastSetNum && ms.lastSetNum > 0 {
		if ms.lastHomeGames > ms.lastAwayGames {
			ms.setsHome++
		} else if ms.lastAwayGames > ms.lastHomeGames {
			ms.setsAway++
		} else if ms.lastScorer != 0 {
			// Tiebreak set: game counts equal, winner is last point's scorer
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

func (g *Generator) processPoint(p store.Point) {
	pointKey := fmt.Sprintf("%d:%d:%d", p.SetNumber, p.GameNumber, p.PointNumber)
	g.mu.Lock()
	if g.seenPoints[p.MatchTicker] == nil {
		g.seenPoints[p.MatchTicker] = make(map[string]bool)
	}
	if g.seenPoints[p.MatchTicker][pointKey] {
		g.mu.Unlock()
		return
	}
	g.seenPoints[p.MatchTicker][pointKey] = true
	g.mu.Unlock()

	mp := g.detectMatchPoint(p)
	if mp == nil {
		return
	}

	// Only fire when the match-point player is serving.
	// Backtest: serving MP converts 97.3%, returning MP 88.5% but high variance.
	// mp.winner == 1 means home has MP; server == 1 means home is serving.
	isServing := (mp.winner == 1 && p.Server == 1) || (mp.winner == 2 && p.Server == 2)
	if !isServing {
		g.log.Debug("signal: match point but not serving, skipping",
			"match", p.MatchTicker, "winner", mp.winner, "server", p.Server,
			"context", mp.context)
		return
	}

	g.mu.RLock()
	mktTickers, ok := g.markets[p.MatchTicker]
	g.mu.RUnlock()
	if !ok || len(mktTickers) < 2 {
		return
	}

	var marketTicker string
	if mp.winner == 1 {
		marketTicker = mktTickers[0]
	} else {
		marketTicker = mktTickers[1]
	}

	// Empirical conversion rate — not the hand-tuned formula.
	convProb := serveConvProb

	g.mu.RLock()
	mktPrice := g.prices[marketTicker]
	priceTime := g.priceTimes[marketTicker]
	g.mu.RUnlock()
	if mktPrice <= 0 {
		g.log.Debug("signal: no price for market", "market", marketTicker)
		return
	}
	if time.Since(priceTime) > priceStaleTTL {
		g.log.Debug("signal: stale price", "market", marketTicker, "age", time.Since(priceTime))
		return
	}

	edgeCents := int((convProb-mktPrice)*100 + 1e-9)
	if edgeCents < minEdgeCents {
		return
	}

	// Buy only — never sell at match points.
	// Backtest: selling (comeback bets) has 7.1% hit rate, catastrophic PnL.
	size := suggestedSize(edgeCents)

	payload, _ := json.Marshal(map[string]any{
		"home_games": p.HomeGames, "away_games": p.AwayGames,
		"home_points": p.HomePoints, "away_points": p.AwayPoints,
		"server": p.Server, "scorer": p.Scorer,
		"set": p.SetNumber, "game": p.GameNumber,
		"serving": true,
	})

	o := store.Order{
		TS:            time.Now().UnixMilli(),
		MatchTicker:   p.MatchTicker,
		MarketTicker:  marketTicker,
		Action:        "buy",
		Context:       mp.context,
		ConvProb:      convProb,
		MarketPrice:   mktPrice,
		EdgeCents:     edgeCents,
		SuggestedSize: size,
		SetNumber:     p.SetNumber,
		Payload:       string(payload),
	}

	if !g.tickWriter.IngestOrder(o) {
		g.log.Warn("signal: order dropped, buffer full", "match", p.MatchTicker, "market", marketTicker)
		return
	}
	g.log.Info("signal: order emitted",
		"match", p.MatchTicker, "market", marketTicker,
		"action", "buy", "edge_cents", edgeCents, "conv_prob", convProb,
		"mkt_price", mktPrice, "size", size, "context", mp.context)
}

type matchPoint struct {
	winner  int // 1=home, 2=away
	context string
}

func (g *Generator) detectMatchPoint(p store.Point) *matchPoint {
	g.mu.RLock()
	ms := g.matchStates[p.MatchTicker]
	var setsHome, setsAway int
	if ms != nil {
		setsHome = ms.setsHome
		setsAway = ms.setsAway
	}
	g.mu.RUnlock()
	gamesHome, gamesAway := p.HomeGames, p.AwayGames

	homeNeedsSet := setsToWin - setsHome
	awayNeedsSet := setsToWin - setsAway
	if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
		return nil
	}

	homeOneSetAway := homeNeedsSet == 1
	awayOneSetAway := awayNeedsSet == 1

	var homeMatchPoint, awayMatchPoint bool

	// Skip tiebreak points — research decision: don't factor in tiebreakers.
	// Set counting still handles TB sets via lastScorer in updateMatchState.
	if p.IsTiebreak {
		return nil
	}

	// Regular game: receiver can win game with next point
	homeCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 1)
	awayCanWinGame := canWinGame(p.HomePoints, p.AwayPoints, p.Server, 2)

	if homeOneSetAway && homeCanWinGame && gamesHome >= gamesPerSet-1 && gamesHome > gamesAway {
		homeMatchPoint = true
	}
	if awayOneSetAway && awayCanWinGame && gamesAway >= gamesPerSet-1 && gamesAway > gamesHome {
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

	return &matchPoint{
		winner:  winner,
		context: ctx,
	}
}

func canWinGame(homePts, awayPts string, server, player int) bool {
	h := normalizeScore(homePts)
	a := normalizeScore(awayPts)
	if player == 1 {
		// Home can win game with next point if: advantage, or 40 vs <40
		return h == "A" || (h == "40" && a != "40" && a != "A")
	}
	return a == "A" || (a == "40" && h != "40" && h != "A")
}

func normalizeScore(s string) string {
	switch s {
	case "0", "15", "30", "40", "A":
		return s
	default:
		return ""
	}
}

func suggestedSize(absEdgeCents int) float64 {
	size := baseSize * float64(absEdgeCents) / float64(minEdgeCents)
	if size > maxSize {
		size = maxSize
	}
	return size
}

// DeletePrice removes a single market's price tracking state.
// Called by tracker on unsubscribe to prevent unbounded growth when
// FlashScore is disabled (UnregisterMarkets is only called from FlashScore).
func (g *Generator) DeletePrice(marketTicker string) {
	g.mu.Lock()
	delete(g.prices, marketTicker)
	delete(g.priceTimes, marketTicker)
	g.mu.Unlock()
}

func (g *Generator) GetPrice(marketTicker string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.prices[marketTicker]
}

// GetPriceAge returns how long ago the price was last updated.
// Returns a large duration if no price exists (stale/missing).
func (g *Generator) GetPriceAge(marketTicker string) time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()
	t, ok := g.priceTimes[marketTicker]
	if !ok {
		return time.Hour
	}
	return time.Since(t)
}

func (g *Generator) String() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return fmt.Sprintf("signal.Generator{markets=%d, prices=%d, states=%d}",
		len(g.markets), len(g.prices), len(g.matchStates))
}
