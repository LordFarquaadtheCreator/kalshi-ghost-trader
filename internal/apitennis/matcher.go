package apitennis

import (
	"strings"
	"unicode"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// MatchEventToKalshi tries to match an API-Tennis event to a Kalshi event
// by fuzzy player name matching. Returns the Kalshi event_ticker or "".
//
// API-Tennis names: "S. Bejlek", "R. Zarazua", "P. Verbin"
// Kalshi event titles: "Bejlek vs Zarazua"
// Kalshi market player_name: "Sara Bejlek"
func MatchEventToKalshi(ev WSEvent, kalshiEvents []store.Event) string {
	homeLN := normalizeLastName(extractLastName(ev.EventFirstPlayer))
	awayLN := normalizeLastName(extractLastName(ev.EventSecondPlayer))
	if homeLN == "" || awayLN == "" {
		return ""
	}

	for _, ke := range kalshiEvents {
		kHome, kAway := parseKalshiTitle(ke.Title)
		if normalizeLastName(kHome) == homeLN && normalizeLastName(kAway) == awayLN {
			return ke.EventTicker
		}
		// Try reversed order (Kalshi might list away first)
		if normalizeLastName(kAway) == homeLN && normalizeLastName(kHome) == awayLN {
			return ke.EventTicker
		}
	}
	return ""
}

// orderMarketsByPlayerNames returns market tickers ordered [first, second]
// by matching API-Tennis player names to Kalshi market player_name.
func orderMarketsByPlayerNames(firstPlayer, secondPlayer string, markets []store.Market) []string {
	if len(markets) < 2 {
		var tickers []string
		for _, m := range markets {
			tickers = append(tickers, m.MarketTicker)
		}
		return tickers
	}

	firstLN := normalizeLastName(extractLastName(firstPlayer))
	secondLN := normalizeLastName(extractLastName(secondPlayer))

	var firstTicker, secondTicker string
	for _, m := range markets {
		mktLN := normalizeLastName(extractLastName(m.PlayerName))
		if mktLN == firstLN && firstTicker == "" {
			firstTicker = m.MarketTicker
		} else if mktLN == secondLN && secondTicker == "" {
			secondTicker = m.MarketTicker
		}
	}

	if firstTicker != "" && secondTicker != "" {
		return []string{firstTicker, secondTicker}
	}
	return []string{markets[0].MarketTicker, markets[1].MarketTicker}
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
// "S. Bejlek" → "Bejlek", "Alexandre Muller" → "Muller", "Muller A." → "Muller"
func extractLastName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if i := strings.Index(name, " ("); i >= 0 {
		name = name[:i]
	}
	// Doubles: "Furlanetto/Parizzia" → "Furlanetto Parizzia"
	name = strings.ReplaceAll(name, "/", " ")
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	last = strings.TrimSuffix(last, ".")
	if len(last) <= 2 && unicode.IsLetter(rune(last[0])) {
		return parts[0]
	}
	return last
}

// normalizeLastName lowercases and strips accents for matching.
func normalizeLastName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
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
