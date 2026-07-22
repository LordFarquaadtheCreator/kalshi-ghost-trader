// Package apitennis implements the API-Tennis WebSocket score feed adapter.
// Connects to wss://wss.api-tennis.com/live, parses point-by-point updates,
// and forwards PointScored events to the caller via the onPoint callback.
package apitennis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

// Scraper connects to API-Tennis WS and implements ports.ScoreFeed.
type Scraper struct {
	url      string
	log      *slog.Logger
	mu       sync.Mutex
	tracking map[string]bool // event_tickers being tracked
}

// New creates an API-Tennis scraper.
func New(apiKey, timezone string, log *slog.Logger) *Scraper {
	if timezone == "" {
		timezone = "+00:00"
	}
	url := fmt.Sprintf("wss://wss.api-tennis.com/live?APIkey=%s&timezone=%s", apiKey, timezone)
	return &Scraper{
		url:      url,
		log:      log,
		tracking: make(map[string]bool),
	}
}

// Run starts the WS read loop. Calls onPoint for each new point scored.
// Auto-reconnects with exponential backoff.
func (s *Scraper) Run(ctx context.Context, onPoint func(match.PointScored)) error {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		conn, _, err := websocket.Dial(ctx, s.url, &websocket.DialOptions{
			HTTPHeader: http.Header{},
		})
		if err != nil {
			s.log.Warn("apitennis: dial failed", "err", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		conn.SetReadLimit(1 << 20) // 1MB — long matches exceed 32KB
		s.log.Info("apitennis: connected")
		backoff = 1 * time.Second

		for {
			if ctx.Err() != nil {
				conn.Close(websocket.StatusNormalClosure, "shutdown")
				return ctx.Err()
			}
			_, data, err := conn.Read(ctx)
			if err != nil {
				s.log.Warn("apitennis: read failed, reconnecting", "err", err)
				conn.Close(websocket.StatusGoingAway, "read error")
				break
			}
			s.handleMessage(data, onPoint)
		}
	}
}

// StartPolling marks an event as being tracked.
func (s *Scraper) StartPolling(eventTicker string) error {
	s.mu.Lock()
	s.tracking[eventTicker] = true
	s.mu.Unlock()
	return nil
}

// StopPolling removes an event from tracking.
func (s *Scraper) StopPolling(eventTicker string) error {
	s.mu.Lock()
	delete(s.tracking, eventTicker)
	s.mu.Unlock()
	return nil
}

func (s *Scraper) handleMessage(data []byte, onPoint func(match.PointScored)) {
	// API-Tennis may send a single object or an array.
	var events []wsEvent

	// Try array first
	if err := json.Unmarshal(data, &events); err != nil {
		// Try single object
		var single wsEvent
		if err := json.Unmarshal(data, &single); err != nil {
			return // skip malformed
		}
		events = []wsEvent{single}
	}

	now := time.Now().UnixMilli()
	for _, ev := range events {
		if ev.EventLive != "1" {
			continue
		}
		// Forward all points from the latest state.
		// API-Tennis sends full point-by-point on every push; the merger
		// in the match loop dedupes.
		for _, set := range ev.PointByPoint {
			setNum := parseSetNumber(set.SetNumber)
			gameNum := parseInt(set.NumberGame)
			server := parseServer(set.PlayerServed)

			homeGames, awayGames := parseGameScore(set.Score)

			for _, pt := range set.Points {
				homePts, awayPts := parseScore(pt.Score)
				pointNum := parseInt(pt.NumberPoint)
				scorer := parseServer(pt.Score) // approximate: scorer from score direction

				// Determine scorer: if home points increased, home scored.
				// This is approximate — the merger dedupes by score state.
				scorer = 0
				if homePts == "40" || homePts == "A" {
					if awayPts == "0" || awayPts == "15" || awayPts == "30" {
						scorer = 1
					}
				}
				if awayPts == "40" || awayPts == "A" {
					if homePts == "0" || homePts == "15" || homePts == "30" {
						scorer = 2
					}
				}

				isBP := pt.BreakPoint != nil && *pt.BreakPoint != "" && *pt.BreakPoint != "0"
				isSP := pt.SetPoint != nil && *pt.SetPoint != "" && *pt.SetPoint != "0"
				isMP := pt.MatchPoint != nil && *pt.MatchPoint != "" && *pt.MatchPoint != "0"

				point := match.Point{
					EventTicker:  "",
					TS:           now,
					RecvTS:       now,
					SetNumber:    setNum,
					GameNumber:   gameNum,
					PointNumber:  pointNum,
					Server:       server,
					Scorer:       scorer,
					HomePoints:   homePts,
					AwayPoints:   awayPts,
					HomeGames:    homeGames,
					AwayGames:    awayGames,
					IsBreakPoint: isBP,
					IsSetPoint:   isSP,
					IsMatchPoint: isMP,
				}

				onPoint(match.PointScored{
					EventTicker: "",
					Point:       point,
					TS:          now,
				})
			}
		}
	}
}

// --- types ---

type wsEvent struct {
	EventKey          int       `json:"event_key"`
	EventFirstPlayer  string    `json:"event_first_player"`
	EventSecondPlayer string    `json:"event_second_player"`
	EventServe        string    `json:"event_serve"`
	EventStatus       string    `json:"event_status"`
	EventLive         string    `json:"event_live"`
	PointByPoint      []setData `json:"pointbypoint"`
}

type setData struct {
	SetNumber    string      `json:"set_number"`
	NumberGame   string      `json:"number_game"`
	PlayerServed string      `json:"player_served"`
	ServeWinner  string      `json:"serve_winner"`
	Score        string      `json:"score"`
	Points       []pointData `json:"points"`
}

type pointData struct {
	NumberPoint string  `json:"number_point"`
	Score       string  `json:"score"`
	BreakPoint  *string `json:"break_point"`
	SetPoint    *string `json:"set_point"`
	MatchPoint  *string `json:"match_point"`
}

// --- helpers ---

func parseServer(s string) int {
	if s == "First Player" {
		return 1
	}
	return 2
}

func parseSetNumber(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func parseScore(s string) (home, away string) {
	parts := strings.SplitN(s, " - ", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func parseGameScore(s string) (home, away int) {
	parts := strings.SplitN(s, " - ", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	home, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	away, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	return
}

// Ensure interface compliance.
var _ ports.ScoreFeed = (*Scraper)(nil)
