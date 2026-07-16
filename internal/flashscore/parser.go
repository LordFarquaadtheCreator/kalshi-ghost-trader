package flashscore

import (
	"regexp"
	"strings"
)

// bMarkerRe matches |B1|, |B2|, |B3|, etc. — break point markers in HL field.
var bMarkerRe = regexp.MustCompile(`\|B\d\|`)

// Feed delimiters used by FlashScore's internal API.
const (
	rowSep  = "~"
	cellSep = "¬"
	kvSep   = "÷"
)

// Feed field codes.
const (
	fEventID     = "AA" // match ID
	fHomeName    = "CX" // home player display name
	fHomeAlt     = "AE" // home player (alternate field)
	fAwayName    = "AF" // away player display name
	fAwayAlt     = "FK" // away player (alternate field)
	fHomeID      = "PX" // home player internal ID
	fAwayID      = "PY" // away player internal ID
	fHomeKW      = "WU" // home player URL keyword
	fAwayKW      = "WV" // away player URL keyword
	fStartTS     = "AD" // match start unix seconds
	fStageType   = "AB" // 1=finished, 2=in-progress, 3=upcoming
	fStageID     = "AC" // stage ID
	fTournament  = "ZA" // tournament name
	fSetHeader   = "HA" // "Set N"
	fPointsLabel = "HB" // "Point by point - Set N" or "Tiebreak - Set N"
	fHomeGames   = "HC" // home games AFTER this game (or home TB points)
	fAwayGames   = "HE" // away games AFTER this game (or away TB points)
	fServer      = "HG" // 1=home serves, 2=away serves
	fHomeFinal   = "HH" // home final (tiebreak sub-score)
	fAwayFinal   = "HF" // away final
	fBreakFlag   = "HK" // break point indicator
	fPointSeq    = "HL" // comma-separated point scores "0:15,0:30,..."
	fEndMarker   = "A1" // end of data
)

// ParseDailyFeed parses the f_2_* daily feed into a list of matches.
// Only tennis singles matches are returned (doubles/exhibitions filtered).
func ParseDailyFeed(feed string) []FeedMatch {
	var matches []FeedMatch
	var currentCategory, currentSurface, currentTournament string

	rows := strings.Split(feed, rowSep)
	for _, row := range rows {
		cells := strings.Split(row, cellSep)
		if len(cells) == 0 {
			continue
		}

		fields := parseFields(cells)
		if len(fields) == 0 {
			continue
		}

		// Tournament header row starts with ZA
		if za, ok := fields[fTournament]; ok {
			currentCategory, currentTournament, currentSurface = parseTournament(za)
			continue
		}

		// Match row starts with AA
		id, ok := fields[fEventID]
		if !ok {
			continue
		}

		// Skip doubles, exhibitions, juniors
		upper := strings.ToUpper(currentTournament)
		if strings.Contains(upper, "DOUBLES") ||
			strings.Contains(upper, "EXHIBITION") ||
			strings.Contains(upper, "JUNIOR") {
			continue
		}

		home := firstNonEmpty(fields[fHomeName], fields[fHomeAlt])
		away := firstNonEmpty(fields[fAwayName], fields[fAwayAlt])
		if home == "" || away == "" {
			continue
		}

		m := FeedMatch{
			ID:         id,
			HomeName:   stripParens(home),
			AwayName:   stripParens(away),
			HomeID:     fields[fHomeID],
			AwayID:     fields[fAwayID],
			HomeKW:     fields[fHomeKW],
			AwayKW:     fields[fAwayKW],
			Tournament: currentTournament,
			Category:   currentCategory,
			Surface:    currentSurface,
			StartTS:    int64(parseInt(fields[fStartTS])),
			StageType:  parseInt(fields[fStageType]),
			StageID:    parseInt(fields[fStageID]),
		}
		matches = append(matches, m)
	}
	return matches
}

// parseTournament extracts category, name, and surface from the ZA field.
// Format: "ATP - SINGLES: Bastad (Sweden) - Qualification, clay"
// Returns: ("ATP", "Bastad (Sweden) - Qualification", "clay")
func parseTournament(za string) (category, name, surface string) {
	za = strings.TrimSpace(za)
	parts := strings.SplitN(za, " - ", 2)
	if len(parts) < 2 {
		return "", za, ""
	}
	category = strings.TrimSpace(parts[0])
	rest := strings.TrimSpace(parts[1])

	colonIdx := strings.Index(rest, ": ")
	if colonIdx >= 0 {
		name = strings.TrimSpace(rest[colonIdx+2:])
	} else {
		name = rest
	}

	if commaIdx := strings.LastIndex(name, ", "); commaIdx >= 0 {
		surface = strings.TrimSpace(name[commaIdx+2:])
		name = strings.TrimSpace(name[:commaIdx])
	}
	return category, name, surface
}

// ParsePointByPoint parses the df_mh_1 response into structured point data.
// HC/HE in game rows = game score AFTER the game. Game winner derived by
// comparing to previous row's game score (or 0-0 for first game in set).
// HL = comma-separated "home:away" point scores within the game.
// Tiebreak rows: HC/HE = TB point score, one row per TB point.
// The tiebreak is one game (gameNum = last game + 1); each row is a point
// within that game. Set game score comes from the summary row, not TB points.
func ParsePointByPoint(feed, fsMatchID string) MatchPoints {
	mp := MatchPoints{FSMatchID: fsMatchID}
	var currentSet *SetPoints
	var prevHomeGames, prevAwayGames int
	var gameNum int
	var tbPointNum int
	var inTiebreak bool

	rows := strings.Split(feed, rowSep)
	for _, row := range rows {
		cells := strings.Split(row, cellSep)
		if len(cells) == 0 {
			continue
		}
		fields := parseFields(cells)
		if len(fields) == 0 {
			continue
		}

		// Set header: HA÷Set N
		if ha, ok := fields[fSetHeader]; ok {
			if currentSet != nil {
				mp.Sets = append(mp.Sets, *currentSet)
			}
			setNum := parseSetNumber(ha)
			currentSet = &SetPoints{SetNumber: setNum}
			prevHomeGames = 0
			prevAwayGames = 0
			gameNum = 0
			tbPointNum = 0
			inTiebreak = false
			continue
		}

		// Points/tiebreak label
		if hb, ok := fields[fPointsLabel]; ok {
			inTiebreak = strings.Contains(strings.ToLower(hb), "tiebreak")
			if inTiebreak {
				// Tiebreak is the next game after the last regular game
				gameNum++
				tbPointNum = 0
				prevHomeGames = 0
				prevAwayGames = 0
			}
			continue
		}

		// End marker
		if _, ok := fields[fEndMarker]; ok {
			if currentSet != nil {
				mp.Sets = append(mp.Sets, *currentSet)
				currentSet = nil
			}
			continue
		}

		// Point data row
		hc, hasHC := fields[fHomeGames]
		he, hasHE := fields[fAwayGames]
		if !hasHC || !hasHE || currentSet == nil {
			continue
		}

		homeScore := parseInt(hc)
		awayScore := parseInt(he)
		server := parseInt(fields[fServer])

		if inTiebreak {
			// Tiebreak: one row per point. HC/HE = TB point count.
			// Do NOT overwrite set's game score — TB points are not games.
			tbPointNum++
			scorer := 0
			if homeScore > prevHomeGames {
				scorer = 1
			} else if awayScore > prevAwayGames {
				scorer = 2
			}
			pt := PointData{
				SetNumber:   currentSet.SetNumber,
				GameNumber:  gameNum,
				PointNumber: tbPointNum,
				Server:      server,
				Scorer:      scorer,
				HomePoints:  hc,
				AwayPoints:  he,
				HomeGames:   currentSet.HomeGames,
				AwayGames:   currentSet.AwayGames,
				IsTiebreak:  true,
				RawHL:       "",
			}
			currentSet.Points = append(currentSet.Points, pt)
			prevHomeGames = homeScore
			prevAwayGames = awayScore
			continue
		}

		hl := fields[fPointSeq]
		if hl == "" {
			// Set summary row (final score only, no point detail)
			currentSet.HomeGames = homeScore
			currentSet.AwayGames = awayScore
			prevHomeGames = homeScore
			prevAwayGames = awayScore
			continue
		}

		// Regular game: parse HL point sequence
		gameNum++
		gameWinner := 0
		if homeScore > prevHomeGames {
			gameWinner = 1
		} else if awayScore > prevAwayGames {
			gameWinner = 2
		}

		pts := parseGamePoints(hl, currentSet.SetNumber, gameNum,
			prevHomeGames, prevAwayGames, server, gameWinner)
		currentSet.Points = append(currentSet.Points, pts...)

		prevHomeGames = homeScore
		prevAwayGames = awayScore
		currentSet.HomeGames = homeScore
		currentSet.AwayGames = awayScore
	}

	if currentSet != nil {
		mp.Sets = append(mp.Sets, *currentSet)
	}
	return mp
}

// parseGamePoints parses an HL field like "0:15, 0:30, 0:40, 15:40"
// into individual PointData entries. Format is home:away always.
// gameWinner is the known winner of this game (from HC/HE delta).
// First point scorer derived by comparing to initial "0:0" state.
func parseGamePoints(hl string, setNum, gameNum, homeGames, awayGames, server, gameWinner int) []PointData {
	scores := splitScores(hl)
	var pts []PointData
	// Start from 0:0 — first point's scorer derived by comparing to this
	prevHome, prevAway := "0", "0"

	for i, s := range scores {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 {
			continue
		}
		homePts := strings.TrimSpace(parts[0])
		awayPts := strings.TrimSpace(parts[1])

		scorer := deriveScorer(prevHome, prevAway, homePts, awayPts)

		// Last point: scorer = game winner
		isLast := i == len(scores)-1 || (i == len(scores)-2 && i+1 < len(scores) && strings.TrimSpace(scores[i+1]) == "")
		if isLast {
			scorer = gameWinner
		}

		isBP := isBreakPoint(homePts, awayPts, server)

		pts = append(pts, PointData{
			SetNumber:    setNum,
			GameNumber:   gameNum,
			PointNumber:  i + 1,
			Server:       server,
			Scorer:       scorer,
			HomePoints:   homePts,
			AwayPoints:   awayPts,
			HomeGames:    homeGames,
			AwayGames:    awayGames,
			IsTiebreak:   false,
			IsBreakPoint: isBP,
			RawHL:        hl,
		})
		prevHome = homePts
		prevAway = awayPts
	}
	return pts
}

// splitScores splits the HL field by comma, handling the B1/B2 break markers.
// FlashScore uses pipe-delimited markers WITHIN the HL field:
//   "30:40|B1|, 40:40, 40:A"  — B1 = break point converted
//   "30:40|B1| |B2|"           — B1 + B2 = multiple breaks
// Strip the full |B1|/|B2| markers (pipes included) before splitting.
func splitScores(hl string) []string {
	hl = bMarkerRe.ReplaceAllString(hl, "")
	hl = strings.ReplaceAll(hl, " ,", ",")
	hl = strings.ReplaceAll(hl, ",  ", ", ")
	hl = strings.TrimSpace(hl)
	hl = strings.TrimSuffix(hl, ",")
	hl = strings.TrimPrefix(hl, ",")
	return strings.Split(hl, ",")
}

// deriveScorer determines who won the point by comparing consecutive scores.
func deriveScorer(prevHome, prevAway, curHome, curAway string) int {
	ph := normalizeScore(prevHome)
	pa := normalizeScore(prevAway)
	ch := normalizeScore(curHome)
	ca := normalizeScore(curAway)

	// Handle deuce cycling: 40:40 → A:40 (home scored) or 40:A → 40:40 (home scored back)
	if ch != ph && ch != "" {
		return 1
	}
	if ca != pa && ca != "" {
		return 2
	}
	return 0
}

// isBreakPoint returns true if the receiver is one point away from winning.
func isBreakPoint(homePts, awayPts string, server int) bool {
	h := normalizeScore(homePts)
	a := normalizeScore(awayPts)
	if server == 1 {
		// Away is receiver. Break if away can win with next point.
		return a == "40" && h != "A" || a == "A"
	}
	// Home is receiver
	return h == "40" && a != "A" || h == "A"
}

// normalizeScore converts score strings to comparable form.
func normalizeScore(s string) string {
	s = strings.TrimSpace(s)
	switch s {
	case "0", "15", "30", "40", "A":
		return s
	case "AD":
		return "A"
	default:
		return s
	}
}

// ParseMatchStatus extracts the stage type from a dc_1 response.
// dc_1 uses AZ for stage type (1=finished, 2=in-progress, 3=upcoming),
// unlike the daily feed which uses AB.
func ParseMatchStatus(feed string) int {
	for _, row := range strings.Split(feed, rowSep) {
		fields := parseFields(strings.Split(row, cellSep))
		if az, ok := fields["AZ"]; ok {
			return parseInt(az)
		}
	}
	return 0
}

// parseSetNumber extracts the set number from "Set N".
func parseSetNumber(ha string) int {
	ha = strings.TrimSpace(ha)
	ha = strings.TrimPrefix(ha, "Set ")
	return parseInt(ha)
}

// parseFields converts a cell slice into a field map.
func parseFields(cells []string) map[string]string {
	fields := make(map[string]string, len(cells))
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			continue
		}
		idx := strings.Index(cell, kvSep)
		if idx < 0 {
			continue
		}
		key := cell[:idx]
		val := cell[idx+len(kvSep):]
		fields[key] = val
	}
	return fields
}

// stripParens removes trailing " (Q)" or " (WC)" qualifiers from player names.
func stripParens(s string) string {
	if i := strings.Index(s, " ("); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
