package algorithms

// ClassifyPoint determines whether a point is a set point, match point,
// or break point given the current match context.
// Standalone — usable by scraper, backfill, and strategies.
type PointContext struct {
	SetsHome    int // sets won by home so far
	SetsAway    int // sets won by away so far
	HomeGames   int // games in current set
	AwayGames   int
	HomePoints  string // "0","15","30","40","A"
	AwayPoints  string
	Server      int // 1=home, 2=away
	IsTiebreak  bool
}

type PointClassification struct {
	IsSetPoint   bool
	IsMatchPoint bool
	IsBreakPoint bool
}

func ClassifyPoint(ctx PointContext) PointClassification {
	var c PointClassification

	homeCanWinGame := canWinGame(ctx.HomePoints, ctx.AwayPoints, ctx.Server, 1)
	awayCanWinGame := canWinGame(ctx.HomePoints, ctx.AwayPoints, ctx.Server, 2)

	// Break point: returner can win the game
	if ctx.Server == 1 && awayCanWinGame {
		c.IsBreakPoint = true
	}
	if ctx.Server == 2 && homeCanWinGame {
		c.IsBreakPoint = true
	}

	if ctx.IsTiebreak {
		return c
	}

	homeNeedsSet := setsToWin - ctx.SetsHome
	awayNeedsSet := setsToWin - ctx.SetsAway
	if homeNeedsSet <= 0 || awayNeedsSet <= 0 {
		return c
	}

	homeCanWinSet := homeCanWinGame && ctx.HomeGames >= gamesPerSet-1 && ctx.HomeGames > ctx.AwayGames
	awayCanWinSet := awayCanWinGame && ctx.AwayGames >= gamesPerSet-1 && ctx.AwayGames > ctx.HomeGames

	if !homeCanWinSet && !awayCanWinSet {
		return c
	}

	c.IsSetPoint = true

	if homeCanWinSet && homeNeedsSet == 1 {
		c.IsMatchPoint = true
	}
	if awayCanWinSet && awayNeedsSet == 1 {
		c.IsMatchPoint = true
	}

	return c
}
