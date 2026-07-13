package flashscore

import (
	"strings"
	"unicode"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// MatchEventsToFSMatches tries to map unmapped FlashScore matches to Kalshi
// events by fuzzy player name matching. Returns mapping updates.
// Kalshi event titles look like "Muller vs Shevchenko".
// Kalshi markets have player_name like "Alexandre Muller".
// FlashScore uses "Muller A." or "Alexandre Muller".
func MatchEventsToFSMatches(events []store.Event, fsMatches []store.FSMatch) map[string]string {
	// map: fsMatchID -> eventTicker
	result := make(map[string]string)

	// Build lookup: normalized last name -> Kalshi event
	kalshiByLastName := make(map[string][]store.Event)
	for _, ev := range events {
		home, away := parseKalshiTitle(ev.Title)
		for _, ln := range []string{home, away} {
			key := normalizeLastName(ln)
			if key != "" {
				kalshiByLastName[key] = append(kalshiByLastName[key], ev)
			}
		}
	}

	for _, fsm := range fsMatches {
		homeLN := normalizeLastName(extractLastName(fsm.HomePlayer))
		awayLN := normalizeLastName(extractLastName(fsm.AwayPlayer))
		if homeLN == "" || awayLN == "" {
			continue
		}

		homeEvents := kalshiByLastName[homeLN]
		awayEvents := kalshiByLastName[awayLN]
		if len(homeEvents) == 0 || len(awayEvents) == 0 {
			continue
		}

		// Find event that appears in both home and away last name lookups
		for _, he := range homeEvents {
			for _, ae := range awayEvents {
				if he.EventTicker == ae.EventTicker {
					result[fsm.FSMatchID] = he.EventTicker
					break
				}
			}
		}
	}
	return result
}

// parseKalshiTitle splits "Muller vs Shevchenko" into ("Muller", "Shevchenko").
func parseKalshiTitle(title string) (home, away string) {
	parts := strings.SplitN(title, " vs ", 2)
	if len(parts) != 2 {
		parts = strings.SplitN(title, " v ", 2)
	}
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// extractLastName gets the last name from a player name.
// "Alexandre Muller" → "Muller", "Muller A." → "Muller".
func extractLastName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Remove trailing qualifiers like "(Q)", "(WC)", "(LL)"
	if i := strings.Index(name, " ("); i >= 0 {
		name = name[:i]
	}
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	// Last name is typically the last word, but FlashScore uses
	// "LastName Initial." format. Take first word if it looks like a last name.
	// "Muller A." → first word "Muller"
	// "Alexandre Muller" → last word "Muller"
	// Heuristic: if last word is 1-2 chars + dot, it's an initial → use first word
	last := parts[len(parts)-1]
	last = strings.TrimSuffix(last, ".")
	if len(last) <= 2 && unicode.IsLetter(rune(last[0])) {
		// Likely an initial — use first word as last name
		return parts[0]
	}
	return last
}

// normalizeLastName lowercases and removes accents/diacritics for matching.
func normalizeLastName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Simple accent stripping for common tennis name diacritics
	replacements := map[string]string{
		"é": "e", "è": "e", "ê": "e", "ë": "e",
		"á": "a", "à": "a", "â": "a", "ä": "a",
		"í": "i", "ì": "i", "î": "i", "ï": "i",
		"ó": "o", "ò": "o", "ô": "o", "ö": "o", "ø": "o",
		"ú": "u", "ù": "u", "û": "u", "ü": "u",
		"ñ": "n", "ç": "c",
		"š": "s", "ž": "z", "ř": "r", "ć": "c",
		"č": "c", "đ": "d",
	}
	for from, to := range replacements {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}
