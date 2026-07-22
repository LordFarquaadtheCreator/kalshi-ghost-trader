// Package pricing implements the Markov chain tennis win-probability model.
//
// MarkovModel computes win probability from any score state using a
// hierarchical Markov chain: point → game → set → match.
//
// Not safe for concurrent use; owned by a single match loop. The v2 event
// loop guarantees single-threaded access — no mutex needed (v1 required one
// because strategies were called from multiple goroutines).
package pricing

import (
	"math"
	"strconv"
)

const defaultPServe = 0.64

const setsToWin = 2

// MarkovModel holds the server win probability and memoized recursion caches.
type MarkovModel struct {
	pServe  float64 // probability server wins a point
	pReturn float64 // probability returner wins a point (= 1 - pServe)

	// Lazy memoization maps. Populated on first call, reused after.
	// Key layouts:
	//   gameMemo:  [3]int{h, a, pIsServe}
	//   setMemo:   [4]int{gamesH, gamesA, serverIdx, pIsServe}
	//   matchMemo: [2]int{sH, sA}
	//   tbMemo:    [3]int{h, a, homeServingIdx}
	gameMemo  map[[3]int]float64
	setMemo   map[[4]int]float64
	matchMemo map[[2]int]float64
	tbMemo    map[[3]int]float64
}

// NewMarkovModel creates a Markov chain model with default ATP serve probability.
func NewMarkovModel() *MarkovModel {
	return NewMarkovModelWithProb(defaultPServe)
}

// NewMarkovModelWithProb creates a model with a custom serve point probability.
func NewMarkovModelWithProb(pServe float64) *MarkovModel {
	return &MarkovModel{
		pServe:    pServe,
		pReturn:   1 - pServe,
		gameMemo:  make(map[[3]int]float64, 50),
		setMemo:   make(map[[4]int]float64, 256),
		matchMemo: make(map[[2]int]float64, 9),
		tbMemo:    make(map[[3]int]float64, 200),
	}
}

// WinProbability returns the probability that the home player wins the match
// from the given score state.
//
// setsHome, setsAway: sets already won (best of 3, need 2).
// gamesHome, gamesAway: games in current set.
// homePoints, awayPoints: point score strings ("0","15","30","40","A").
// server: 1=home serving, 2=away serving.
// isTiebreak: current game is a tiebreak.
func (m *MarkovModel) WinProbability(setsHome, setsAway int, gamesHome, gamesAway int, homePoints, awayPoints string, server int, isTiebreak bool) float64 {
	if setsHome >= setsToWin {
		return 1.0
	}
	if setsAway >= setsToWin {
		return 0.0
	}

	pHomeGame := m.gameWinProb(homePoints, awayPoints, server == 1, isTiebreak)
	pHomeSet := m.setWinProb(gamesHome, gamesAway, pHomeGame, server, isTiebreak)
	pWinSet := m.matchWinProbFromSetScore(setsHome+1, setsAway)
	pLoseSet := m.matchWinProbFromSetScore(setsHome, setsAway+1)

	return pHomeSet*pWinSet + (1-pHomeSet)*pLoseSet
}

// matchWinProbFromSetScore returns P(home wins match) from set score.
func (m *MarkovModel) matchWinProbFromSetScore(sH, sA int) float64 {
	if sH >= setsToWin {
		return 1.0
	}
	if sA >= setsToWin {
		return 0.0
	}
	key := [2]int{sH, sA}
	if v, ok := m.matchMemo[key]; ok {
		return v
	}

	pSet := 0.5*m.setWinProbCached(0, 0, 1, true) +
		0.5*m.setWinProbCached(0, 0, 2, false)

	result := pSet*m.matchWinProbFromSetScore(sH+1, sA) + (1-pSet)*m.matchWinProbFromSetScore(sH, sA+1)
	m.matchMemo[key] = result
	return result
}

// gameWinProb returns probability that home wins the current game.
func (m *MarkovModel) gameWinProb(homePoints, awayPoints string, homeServing bool, isTiebreak bool) float64 {
	if isTiebreak {
		return m.tiebreakWinProb(homePoints, awayPoints, homeServing)
	}

	h := pointValue(homePoints)
	a := pointValue(awayPoints)
	return m.gameWinProbMemoized(h, a, homeServing)
}

// gameWinProbMemoized returns P(home wins game) from point scores h, a
// with memoization. pIsServe selects pServe (true) or pReturn (false).
func (m *MarkovModel) gameWinProbMemoized(h, a int, pIsServe bool) float64 {
	key := [3]int{h, a, boolToInt(pIsServe)}
	if v, ok := m.gameMemo[key]; ok {
		return v
	}
	p := m.pServe
	if !pIsServe {
		p = m.pReturn
	}
	result := gameWinProbRecursive(h, a, p)
	m.gameMemo[key] = result
	return result
}

// gameWinProbRecursive computes P(home wins game) from point scores h, a.
// p = probability home wins the next point.
func gameWinProbRecursive(h, a int, p float64) float64 {
	if h >= 4 && h-a >= 2 {
		return 1.0
	}
	if a >= 4 && a-h >= 2 {
		return 0.0
	}

	// Deuce: both at 3+ (40-40 or deuce cycle)
	if h >= 3 && a >= 3 {
		p2 := p * p
		q2 := (1 - p) * (1 - p)
		if h == a {
			return p2 / (p2 + q2)
		}
		if h == a+1 {
			return p + (1-p)*p2/(p2+q2)
		}
		if a == h+1 {
			return p * p2 / (p2 + q2)
		}
	}

	return p*gameWinProbRecursive(h+1, a, p) + (1-p)*gameWinProbRecursive(h, a+1, p)
}

// setWinProb returns probability home wins the set.
func (m *MarkovModel) setWinProb(gamesHome, gamesAway int, pHomeGame float64, server int, isTiebreak bool) float64 {
	if gamesHome >= 6 && gamesHome-gamesAway >= 2 {
		return 1.0
	}
	if gamesAway >= 6 && gamesAway-gamesHome >= 2 {
		return 0.0
	}
	if isTiebreak {
		return pHomeGame
	}
	if gamesHome >= 6 && gamesAway >= 6 && gamesHome == gamesAway {
		return m.tiebreakWinProb("0", "0", server == 1)
	}

	nextServer := 2
	if server == 2 {
		nextServer = 1
	}
	nextHomeServes := nextServer == 1

	pWinGame := pHomeGame * m.setWinProbCached(gamesHome+1, gamesAway, nextServer, nextHomeServes)
	pLoseGame := (1 - pHomeGame) * m.setWinProbCached(gamesHome, gamesAway+1, nextServer, nextHomeServes)

	return pWinGame + pLoseGame
}

// setWinProbCached returns setWinProb where pHomeGame is pServe (pIsServe=true)
// or pReturn (pIsServe=false).
func (m *MarkovModel) setWinProbCached(gamesHome, gamesAway, server int, pIsServe bool) float64 {
	if gamesHome >= 6 && gamesHome-gamesAway >= 2 {
		return 1.0
	}
	if gamesAway >= 6 && gamesAway-gamesHome >= 2 {
		return 0.0
	}
	if gamesHome >= 6 && gamesAway >= 6 && gamesHome == gamesAway {
		return m.tiebreakWinProb("0", "0", server == 1)
	}

	key := [4]int{gamesHome, gamesAway, server - 1, boolToInt(pIsServe)}
	if v, ok := m.setMemo[key]; ok {
		return v
	}

	pHomeGame := m.pServe
	if !pIsServe {
		pHomeGame = m.pReturn
	}

	nextServer := 2
	if server == 2 {
		nextServer = 1
	}
	nextHomeServes := nextServer == 1

	pWinGame := pHomeGame * m.setWinProbCached(gamesHome+1, gamesAway, nextServer, nextHomeServes)
	pLoseGame := (1 - pHomeGame) * m.setWinProbCached(gamesHome, gamesAway+1, nextServer, nextHomeServes)

	result := pWinGame + pLoseGame
	m.setMemo[key] = result
	return result
}

// tiebreakWinProb computes probability home wins tiebreak from current score.
// homeServing: who serves the next point in tiebreak.
// Tiebreak: first to 7, win by 2. Serve alternates: 1-2-2-1-1-2-2-1...
func (m *MarkovModel) tiebreakWinProb(homePoints, awayPoints string, homeServing bool) float64 {
	h := tbPointValue(homePoints)
	a := tbPointValue(awayPoints)

	p := m.pServe
	if !homeServing {
		p = m.pReturn
	}

	return m.tbWinProbMemoized(h, a, p, homeServing, h+a)
}

// tbWinProbMemoized computes P(home wins tiebreak) with memoization.
func (m *MarkovModel) tbWinProbMemoized(h, a int, p float64, homeServing bool, totalPoints int) float64 {
	if h >= 7 && h-a >= 2 {
		return 1.0
	}
	if a >= 7 && a-h >= 2 {
		return 0.0
	}
	// Tiebreak deuce: both >= 7 and equal. Closed form prevents infinite
	// recursion.
	//
	// Corrected formula: in a tiebreak, serve alternates every 2 points.
	// From deuce, winning 2 consecutive points means winning one on serve
	// and one on return. P(home wins both) = pServe * pReturn.
	// P(away wins both) = (1-pServe) * (1-pReturn).
	//
	// v1 incorrectly used pAvg² where pAvg = (pServe+pReturn)/2, which
	// assumes the same server for both points. The corrected formula
	// accounts for serve alternation.
	if h >= 6 && a >= 6 && h == a {
		p2 := m.pServe * m.pReturn
		q2 := (1 - m.pServe) * (1 - m.pReturn)
		return p2 / (p2 + q2)
	}

	key := [3]int{h, a, boolToInt(homeServing)}
	if v, ok := m.tbMemo[key]; ok {
		return v
	}

	// Serve alternation: 1,2,2,1,1,2,2,1,...
	var nextHomeServing bool
	if totalPoints%2 == 0 {
		nextHomeServing = !homeServing
	} else {
		nextHomeServing = homeServing
	}

	nextP := m.pServe
	if !nextHomeServing {
		nextP = m.pReturn
	}

	result := p*m.tbWinProbMemoized(h+1, a, nextP, nextHomeServing, totalPoints+1) +
		(1-p)*m.tbWinProbMemoized(h, a+1, nextP, nextHomeServing, totalPoints+1)
	m.tbMemo[key] = result
	return result
}

// FairValue returns the Markov fair-value probability for the home player
// as a price (0-1), clamped to [0.01, 0.99].
func (m *MarkovModel) FairValue(setsHome, setsAway int, gamesHome, gamesAway int, homePoints, awayPoints string, server int, isTiebreak bool) float64 {
	p := m.WinProbability(setsHome, setsAway, gamesHome, gamesAway, homePoints, awayPoints, server, isTiebreak)
	return math.Max(0.01, math.Min(0.99, p))
}

// --- helpers ---

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// pointValue converts tennis point string to numeric value.
func pointValue(s string) int {
	switch s {
	case "0":
		return 0
	case "15":
		return 1
	case "30":
		return 2
	case "40":
		return 3
	case "A":
		return 4
	default:
		return 0
	}
}

// tbPointValue converts tiebreak point string to numeric value.
func tbPointValue(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
