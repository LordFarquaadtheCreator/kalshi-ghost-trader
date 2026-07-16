// Package apitennis implements a real-time tennis data client for api-tennis.com.
//
// Unlike FlashScore's polling approach, API-Tennis provides a WebSocket feed
// that pushes point-by-point updates the moment they happen. This eliminates
// the 10-second polling delay that caused missed match points.
//
// The WS endpoint wss://wss.api-tennis.com/live pushes JSON match updates
// containing full point-by-point data including server, scorer, and
// break/set/match point flags.
package apitennis

// WSEvent is a single match update pushed by the API-Tennis WebSocket.
// One message per match that had a state change (point won, game completed, etc.)
type WSEvent struct {
	EventKey          int     `json:"event_key"`
	EventDate         string  `json:"event_date"`
	EventTime         string  `json:"event_time"`
	EventFirstPlayer  string  `json:"event_first_player"`
	FirstPlayerKey    int     `json:"first_player_key"`
	EventSecondPlayer string  `json:"event_second_player"`
	SecondPlayerKey   int     `json:"second_player_key"`
	EventFinalResult  string  `json:"event_final_result"`
	EventGameResult   string  `json:"event_game_result"`
	EventServe        string  `json:"event_serve"` // "First Player" or "Second Player"
	EventWinner       *string `json:"event_winner"`
	EventStatus       string  `json:"event_status"` // "Set 1", "Finished", etc.
	EventTypeType     string  `json:"event_type_type"`
	TournamentName    string  `json:"tournament_name"`
	TournamentKey     int     `json:"tournament_key"`
	TournamentSeason  string  `json:"tournament_season"`
	EventLive         string  `json:"event_live"` // "1" = live
	PointByPoint      []SetData `json:"pointbypoint"`
	Scores            []ScoreData `json:"scores"`
}

// SetData holds point-by-point data for one set.
type SetData struct {
	SetNumber    string     `json:"set_number"`    // "Set 1"
	NumberGame   string     `json:"number_game"`   // "1"
	PlayerServed string     `json:"player_served"` // "First Player" or "Second Player"
	ServeWinner  string     `json:"serve_winner"`  // "First Player" or "Second Player"
	ServeLost    string     `json:"serve_lost"`
	Score        string     `json:"score"` // "0 - 1" (home games - away games after this game)
	Points       []PointData `json:"points"`
}

// PointData is a single point within a game.
type PointData struct {
	NumberPoint string  `json:"number_point"` // "1", "2", etc.
	Score       string  `json:"score"`        // "15 - 0" (home points - away points)
	BreakPoint  *string `json:"break_point"`
	SetPoint    *string `json:"set_point"`
	MatchPoint  *string `json:"match_point"`
}

// ScoreData is a set score entry.
type ScoreData struct {
	ScoreFirst  string `json:"score_first"`  // home games won in this set
	ScoreSecond string `json:"score_second"` // away games won in this set
	ScoreSet    string `json:"score_set"`    // set number
}

// parseServer converts "First Player"/"Second Player" to 1/2.
func parseServer(s string) int {
	if s == "First Player" {
		return 1
	}
	return 2
}

// parseSetNumber extracts the set number from "Set N" format.
func parseSetNumber(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// parseScore splits "15 - 0" into ("15", "0").
func parseScore(s string) (home, away string) {
	for i := 0; i < len(s)-2; i++ {
		if s[i] == ' ' && s[i+1] == '-' && s[i+2] == ' ' {
			return s[:i], s[i+3:]
		}
	}
	return s, ""
}
