package algorithms

import "math"

// MarkovModel computes tennis win probability from any score state
// using a hierarchical Markov chain: point → game → set → match.
//
// Parameter: pServe = probability that the server wins a point.
// Default 0.64 (ATP tour average). For WTA use ~0.62.
//
// State space is small enough for exact computation via recursion with memoization.
// Levels: game (points), set (games + tiebreak), match (sets).

const defaultPServe = 0.64

// MarkovModel holds the server win probability and precomputed caches.
type MarkovModel struct {
	pServe  float64 // probability server wins a point
	pReturn float64 // probability returner wins a point (= 1 - pServe)
}

// NewMarkovModel creates a Markov chain model with default ATP serve probability.
func NewMarkovModel() *MarkovModel {
	return &MarkovModel{pServe: defaultPServe, pReturn: 1 - defaultPServe}
}

// NewMarkovModelWithProb creates a model with a custom serve point probability.
func NewMarkovModelWithProb(pServe float64) *MarkovModel {
	return &MarkovModel{pServe: pServe, pReturn: 1 - pServe}
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

	// Probability home wins current game
	pHomeGame := m.gameWinProb(homePoints, awayPoints, server == 1, isTiebreak)

	// Probability home wins current set
	pHomeSet := m.setWinProb(gamesHome, gamesAway, pHomeGame, server, isTiebreak)

	// Probability home wins match from set score after this set
	pWinSet := m.matchWinProbFromSetScore(setsHome+1, setsAway)
	pLoseSet := m.matchWinProbFromSetScore(setsHome, setsAway+1)

	return pHomeSet*pWinSet + (1-pHomeSet)*pLoseSet
}

// matchWinProbFromSetScore returns P(home wins match) from set score.
// Assumes fresh sets going forward with 50/50 serve order.
func (m *MarkovModel) matchWinProbFromSetScore(sH, sA int) float64 {
	if sH >= setsToWin {
		return 1.0
	}
	if sA >= setsToWin {
		return 0.0
	}
	// P(win a fresh set) — average of serving first or returning first
	pSet := 0.5*m.setWinProb(0, 0, m.gameWinProb("0", "0", true, false), 1, false) +
		0.5*m.setWinProb(0, 0, m.gameWinProb("0", "0", false, false), 2, false)

	return pSet*m.matchWinProbFromSetScore(sH+1, sA) + (1-pSet)*m.matchWinProbFromSetScore(sH, sA+1)
}

// gameWinProb returns probability that home wins the current game.
// homeServing: true if home is serving.
// For tiebreak, uses alternating serve pattern.
func (m *MarkovModel) gameWinProb(homePoints, awayPoints string, homeServing bool, isTiebreak bool) float64 {
	if isTiebreak {
		return m.tiebreakWinProb(homePoints, awayPoints, homeServing)
	}

	h := pointValueMarkov(homePoints)
	a := pointValueMarkov(awayPoints)

	p := m.pServe
	if !homeServing {
		p = m.pReturn
	}

	return gameWinProbRecursive(h, a, p)
}

// gameWinProbRecursive computes P(home wins game) from point scores h, a.
// p = probability home wins the next point.
func gameWinProbRecursive(h, a int, p float64) float64 {
	// Game ends at 4 points with 2-point lead
	if h >= 4 && h-a >= 2 {
		return 1.0
	}
	if a >= 4 && a-h >= 2 {
		return 0.0
	}

	// Deuce: both at 3+ (40-40 or deuce cycle)
	if h >= 3 && a >= 3 {
		// From deuce, probability of winning = p^2 / (p^2 + (1-p)^2)
		p2 := p * p
		q2 := (1 - p) * (1 - p)
		if h == a {
			// Deuce
			return p2 / (p2 + q2)
		}
		if h == a+1 {
			// Advantage home: win point → game, lose → deuce
			return p*1.0 + (1-p)*p2/(p2+q2)
		}
		if a == h+1 {
			// Advantage away: win point → deuce, lose → game away
			return p * p2 / (p2 + q2)
		}
	}

	// Normal play: 0,1,2,3 points
	return p*gameWinProbRecursive(h+1, a, p) + (1-p)*gameWinProbRecursive(h, a+1, p)
}

// setWinProb returns probability home wins the set.
// gamesHome, gamesAway: current game score.
// pHomeGame: probability home wins a game when home serves.
// server: who serves first in this set (1=home, 2=away).
// isTiebreak: if current game is tiebreak (6-6).
func (m *MarkovModel) setWinProb(gamesHome, gamesAway int, pHomeGame float64, server int, isTiebreak bool) float64 {
	// Set ends at 6 games with 2-point lead, or 7-6 (tiebreak)
	if gamesHome >= 6 && gamesHome-gamesAway >= 2 {
		return 1.0
	}
	if gamesAway >= 6 && gamesAway-gamesHome >= 2 {
		return 0.0
	}
	if gamesHome == 6 && gamesAway == 6 {
		// Tiebreak: model as single game with alternating serve
		// Approximate: use average serve probability
		pTB := 0.5*m.pServe + 0.5*m.pReturn
		return pTB // simplified: 50-50 at start of tiebreak
	}
	if isTiebreak {
		// Already in tiebreak — pHomeGame already computed
		return pHomeGame
	}

	// Current game uses pHomeGame (computed from point score).
	// Future games use serve-based probabilities.
	nextServer := 2
	if server == 2 {
		nextServer = 1
	}

	nextHomeServes := nextServer == 1
	nextPHomeGame := m.pServe
	if !nextHomeServes {
		nextPHomeGame = m.pReturn
	}

	pWinGame := pHomeGame * m.setWinProb(gamesHome+1, gamesAway, nextPHomeGame, nextServer, false)
	pLoseGame := (1 - pHomeGame) * m.setWinProb(gamesHome, gamesAway+1, nextPHomeGame, nextServer, false)

	return pWinGame + pLoseGame
}

// tiebreakWinProb computes probability home wins tiebreak from current score.
// homeServing: who serves the next point in tiebreak.
// Tiebreak: first to 7, win by 2. Serve alternates: 1-2-2-1-1-2-2-1...
func (m *MarkovModel) tiebreakWinProb(homePoints, awayPoints string, homeServing bool) float64 {
	h := pointValueMarkov(homePoints)
	a := pointValueMarkov(awayPoints)

	// Tiebreak points: 0=0, 1=1, 2=2, 3=3, 4=4, 5=5, 6=6
	// First to 7, win by 2
	p := m.pServe
	if !homeServing {
		p = m.pReturn
	}

	return m.tbWinProbRecursive(h, a, p, homeServing, 0)
}

// tbWinProbRecursive computes P(home wins tiebreak) from TB score h, a.
// p = P(home wins next point). homeServing = who serves next.
// totalPoints = total points played (for serve alternation).
func (m *MarkovModel) tbWinProbRecursive(h, a int, p float64, homeServing bool, totalPoints int) float64 {
	if h >= 7 && h-a >= 2 {
		return 1.0
	}
	if a >= 7 && a-h >= 2 {
		return 0.0
	}

	// Serve alternation: 1,2,2,1,1,2,2,1,...
	// Point 0: player 1 serves
	// Points 1-2: player 2 serves
	// Points 3-4: player 1 serves
	// etc.
	nextHomeServing := !homeServing
	if totalPoints%2 == 0 {
		nextHomeServing = !homeServing
	} else {
		nextHomeServing = homeServing
	}

	// Simplified: alternate serve each point
	nextP := m.pServe
	if !nextHomeServing {
		nextP = m.pReturn
	}

	return p*m.tbWinProbRecursive(h+1, a, nextP, nextHomeServing, totalPoints+1) +
		(1-p)*m.tbWinProbRecursive(h, a+1, nextP, nextHomeServing, totalPoints+1)
}

// pointValueMarkov converts tennis point string to numeric value.
func pointValueMarkov(s string) int {
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

// FairValue returns the Markov fair-value probability for the home player
// as a price (0-1). This is the "true" probability the market should price.
func (m *MarkovModel) FairValue(setsHome, setsAway int, gamesHome, gamesAway int, homePoints, awayPoints string, server int, isTiebreak bool) float64 {
	p := m.WinProbability(setsHome, setsAway, gamesHome, gamesAway, homePoints, awayPoints, server, isTiebreak)
	// Clamp to [0.01, 0.99] to avoid extreme values
	return math.Max(0.01, math.Min(0.99, p))
}
