package strategy

// Shared helpers used across strategies. Ported from v1 algorithms package.

// canWinGame returns true if `player` (1=home, 2=away) can win the current
// game from the given point score.
func canWinGame(homePts, awayPts string, server, player int) bool {
	h := normalizeScore(homePts)
	a := normalizeScore(awayPts)
	if player == 1 {
		return h == "A" || (h == "40" && a != "40" && a != "A")
	}
	return a == "A" || (a == "40" && h != "40" && h != "A")
}

// normalizeScore validates a tennis point string.
func normalizeScore(s string) string {
	switch s {
	case "0", "15", "30", "40", "A":
		return s
	default:
		return ""
	}
}

// pointKeyStr builds a dedup key for a point.
func pointKeyStr(set, game, point int) string {
	return intToStr(set) + ":" + intToStr(game) + ":" + intToStr(point)
}

// intToStr converts an int to its decimal string representation.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
