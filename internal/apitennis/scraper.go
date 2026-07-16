package apitennis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Scraper connects to the API-Tennis WebSocket and feeds point-by-point
// updates to per-match worker goroutines. Each active match gets its own
// goroutine — the WS read loop never blocks on algo logic.
//
// Implements tracker.ScorePoller for lifecycle integration.
type Scraper struct {
	ws         *WSClient
	db         *store.DB
	tickWriter *store.TickWriter
	strategy   algorithms.Strategy
	log        *slog.Logger

	workersMu sync.Mutex
	workers   map[string]*matchWorker // event_ticker → worker

	// Cache: API-Tennis event_key → Kalshi event_ticker
	matchCacheMu sync.Mutex
	matchCache   map[int]string

	// Cache: Kalshi event_ticker → market tickers [home, away]
	marketCacheMu sync.Mutex
	marketCache   map[string][]string
}

// matchWorker runs one goroutine per active match. Receives WSEvents
// from the read loop, diffs points, ingests data, and runs the signal
// algo — all without blocking the WS read loop.
type matchWorker struct {
	eventTicker string
	eventKey    int
	ch          chan WSEvent
	done        chan struct{}
	seenKeys    map[string]bool // "set:game:point" → ingested, survives restarts
	tickWriter  *store.TickWriter
	strategy    algorithms.Strategy
	log         *slog.Logger
}

// New creates an API-Tennis scraper.
func New(db *store.DB, tw *store.TickWriter, strat algorithms.Strategy, apiKey, timezone string, log *slog.Logger) *Scraper {
	return &Scraper{
		ws:          NewWSClient(apiKey, timezone, log),
		db:          db,
		tickWriter:  tw,
		strategy:    strat,
		log:         log,
		workers:     make(map[string]*matchWorker),
		matchCache:  make(map[int]string),
		marketCache: make(map[string][]string),
	}
}

// StartPolling is called by the tracker when an event's markets are subscribed.
// Creates a per-match worker goroutine and registers markets with the signal generator.
func (s *Scraper) StartPolling(eventTicker string) {
	seenKeys, err := s.db.GetSeenPointKeys(context.Background(), eventTicker)
	if err != nil {
		s.log.Error("apitennis: load seen keys", "err", err, "event", eventTicker)
		seenKeys = make(map[string]bool)
	}

	w := &matchWorker{
		eventTicker: eventTicker,
		ch:          make(chan WSEvent, 64),
		done:        make(chan struct{}),
		seenKeys:    seenKeys,
		tickWriter:  s.tickWriter,
		strategy:    s.strategy,
		log:         s.log,
	}

	s.workersMu.Lock()
	s.workers[eventTicker] = w
	s.workersMu.Unlock()

	go w.run()

	// Market registration deferred to first WS event — need player names
	// from WSEvent to correctly map [home, away] market order.
	s.log.Info("apitennis worker started", "event", eventTicker)
}

// StopPolling is called by the tracker when an event's markets are unsubscribed.
// Signals the worker goroutine to exit and cleans up.
func (s *Scraper) StopPolling(eventTicker string) {
	s.workersMu.Lock()
	w, ok := s.workers[eventTicker]
	if ok {
		delete(s.workers, eventTicker)
	}
	s.workersMu.Unlock()

	if ok {
		close(w.done)
	}

	if s.strategy != nil {
		s.strategy.UnregisterMarkets(eventTicker)
	}
	s.log.Info("apitennis worker stopped", "event", eventTicker)
}

// Run starts the WebSocket read loop. Blocks until ctx cancelled.
// Auto-reconnects with exponential backoff on disconnect.
func (s *Scraper) Run(ctx context.Context) error {
	s.log.Info("apitennis scraper starting", "ws_url", "wss://wss.api-tennis.com/live")

	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		conn, err := s.ws.Connect(ctx)
		if err != nil {
			s.log.Error("apitennis ws connect failed", "err", err, "attempt", attempt)
			backoff := s.ws.Backoff(attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			attempt++
			continue
		}

		s.log.Info("apitennis ws connected")
		attempt = 0

		err = s.readLoop(ctx, conn)
		conn.Close(websocket.StatusNormalClosure, "shutdown")

		if ctx.Err() != nil {
			s.stopAllWorkers()
			return ctx.Err()
		}

		s.log.Error("apitennis ws disconnected", "err", err)
		select {
		case <-ctx.Done():
			s.stopAllWorkers()
			return ctx.Err()
		case <-time.After(s.ws.minBackoff):
		}
	}
}

// readLoop processes WS messages until error or ctx cancellation.
// Never blocks — dispatches to per-match worker channels.
func (s *Scraper) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		events, err := s.ws.ReadMessage(ctx, conn)
		if err != nil {
			return err
		}
		for _, ev := range events {
			s.dispatch(ev)
		}
	}
}

// dispatch matches a WSEvent to a Kalshi event and sends it to the
// per-match worker channel. Non-blocking — drops if worker channel is full.
func (s *Scraper) dispatch(ev WSEvent) {
	if ev.EventKey == 0 || len(ev.PointByPoint) == 0 {
		return
	}

	// Check cache for event_key → event_ticker mapping
	s.matchCacheMu.Lock()
	eventTicker, cached := s.matchCache[ev.EventKey]
	s.matchCacheMu.Unlock()

	if !cached {
		kalshiEvents, err := s.db.GetAllEventsForMatching(context.Background())
		if err != nil {
			s.log.Error("apitennis: get kalshi events", "err", err)
			return
		}
		eventTicker = MatchEventToKalshi(ev, kalshiEvents)
		s.matchCacheMu.Lock()
		s.matchCache[ev.EventKey] = eventTicker
		s.matchCacheMu.Unlock()
	}

	if eventTicker == "" {
		return
	}

	// Look up worker for this event
	s.workersMu.Lock()
	w, ok := s.workers[eventTicker]
	s.workersMu.Unlock()
	if !ok {
		return // No active worker for this event
	}

	// Register markets on first event for this match (needs player names)
	if s.strategy != nil {
		s.maybeRegisterMarkets(eventTicker, ev)
	}

	// Non-blocking send — drop if worker is busy
	select {
	case w.ch <- ev:
	default:
		s.log.Warn("apitennis: worker channel full, dropping event",
			"event", eventTicker, "event_key", ev.EventKey)
	}
}

// stopAllWorkers closes all worker done channels. Called by Run before returning.
func (s *Scraper) stopAllWorkers() {
	s.workersMu.Lock()
	defer s.workersMu.Unlock()
	for _, w := range s.workers {
		close(w.done)
	}
	s.workers = make(map[string]*matchWorker)
}

// maybeRegisterMarkets orders markets [home, away] using player names from
// the first WSEvent and registers with the strategy. Cached per event.
func (s *Scraper) maybeRegisterMarkets(eventTicker string, ev WSEvent) {
	s.marketCacheMu.Lock()
	cached, ok := s.marketCache[eventTicker]
	s.marketCacheMu.Unlock()

	if ok {
		s.strategy.RegisterMarkets(eventTicker, cached)
		return
	}

	markets, err := s.db.GetMarketsByEvent(context.Background(), eventTicker)
	if err != nil {
		s.log.Error("apitennis: get markets", "err", err, "event", eventTicker)
		return
	}

	// Order [home, away] by matching API-Tennis player names to Kalshi markets.
	// ev.EventFirstPlayer = home (server=1), ev.EventSecondPlayer = away (server=2).
	tickers := orderMarketsByPlayerNames(ev.EventFirstPlayer, ev.EventSecondPlayer, markets)

	s.marketCacheMu.Lock()
	s.marketCache[eventTicker] = tickers
	s.marketCacheMu.Unlock()

	s.strategy.RegisterMarkets(eventTicker, tickers)
	s.log.Info("apitennis: markets registered", "event", eventTicker,
		"home", tickers[0], "away", tickers[1],
		"first_player", ev.EventFirstPlayer, "second_player", ev.EventSecondPlayer)
}

// run is the per-match worker goroutine. Receives WSEvents, diffs points,
// ingests data via TickWriter (async), and runs the signal algo (sync in
// this goroutine — doesn't block the WS read loop or other matches).
func (w *matchWorker) run() {
	for {
		select {
		case <-w.done:
			return
		case ev := <-w.ch:
			w.processEvent(ev)
		}
	}
}

// processEvent flattens points from the WSEvent, dedups via seenKeys,
// and ingests new points.
func (w *matchWorker) processEvent(ev WSEvent) {
	allPoints := flattenPoints(ev)
	if len(allPoints) == 0 {
		return
	}

	now := time.Now().UnixMilli()
	var pts []store.Point
	for _, fp := range allPoints {
		key := fmt.Sprintf("%d:%d:%d", fp.setNumber, fp.gameNumber, fp.pointNumber)
		if w.seenKeys[key] {
			continue
		}
		w.seenKeys[key] = true

		payload, _ := json.Marshal(map[string]any{
			"source":      "apitennis",
			"event_key":   ev.EventKey,
			"set":         fp.setNumber,
			"game":        fp.gameNumber,
			"point":       fp.pointNumber,
			"server":      fp.server,
			"scorer":      fp.scorer,
			"home_points": fp.homePoints,
			"away_points": fp.awayPoints,
			"home_games":  fp.homeGames,
			"away_games":  fp.awayGames,
			"break_point": fp.isBreakPoint,
			"match_point": fp.isMatchPoint,
			"set_point":   fp.isSetPoint,
		})

		pts = append(pts, store.Point{
			MatchTicker:  w.eventTicker,
			FSMatchID:    strconv.Itoa(ev.EventKey),
			TsMs:         now,
			RecvTS:       now,
			SetNumber:    fp.setNumber,
			GameNumber:   fp.gameNumber,
			PointNumber:  fp.pointNumber,
			Server:       fp.server,
			Scorer:       fp.scorer,
			HomePoints:   fp.homePoints,
			AwayPoints:   fp.awayPoints,
			HomeGames:    fp.homeGames,
			AwayGames:    fp.awayGames,
			IsBreakPoint: fp.isBreakPoint,
			IsMatchPoint: fp.isMatchPoint,
			IsSetPoint:   fp.isSetPoint,
			Payload:      string(payload),
		})
	}

	if len(pts) == 0 {
		return
	}

	w.tickWriter.IngestPoints(pts)

	if w.strategy != nil {
		w.strategy.OnPoints(pts)
	}

	w.log.Info("apitennis: new points ingested",
		"event", w.eventTicker, "event_key", ev.EventKey,
		"new", len(pts), "total", len(allPoints))
}

// flattenPoints converts a WSEvent into flattenedPoints with correct running
// game counts computed from ServeWinner. setData.Score is unreliable —
// API-Tennis sends stale/zero values for later games in a set.
func flattenPoints(ev WSEvent) []flattenedPoint {
	var allPoints []flattenedPoint

	type setGames struct{ home, away int }
	setGameCounts := make(map[int]*setGames)

	for _, setData := range ev.PointByPoint {
		setNum := parseSetNumber(setData.SetNumber)
		server := parseServer(setData.PlayerServed)
		gameWinner := parseServer(setData.ServeWinner)

		sg, ok := setGameCounts[setNum]
		if !ok {
			sg = &setGames{}
			setGameCounts[setNum] = sg
		}

		homeGamesInt := sg.home
		awayGamesInt := sg.away

		var prevHome, prevAway string
		for i, pt := range setData.Points {
			homePts, awayPts := parseScore(pt.Score)
			scorer := deriveScorer(prevHome, prevAway, homePts, awayPts, gameWinner, i == len(setData.Points)-1)

			fp := flattenedPoint{
				setNumber:    setNum,
				gameNumber:   parseInt(setData.NumberGame),
				pointNumber:  parseInt(pt.NumberPoint),
				server:       server,
				scorer:       scorer,
				homePoints:   homePts,
				awayPoints:   awayPts,
				homeGames:    homeGamesInt,
				awayGames:    awayGamesInt,
				isBreakPoint: pt.BreakPoint != nil,
				isMatchPoint: pt.MatchPoint != nil,
				isSetPoint:   pt.SetPoint != nil,
			}
			allPoints = append(allPoints, fp)
			prevHome, prevAway = homePts, awayPts
		}

		if gameWinner == 1 {
			sg.home++
		} else if gameWinner == 2 {
			sg.away++
		}
	}

	return allPoints
}

// flattenedPoint is an intermediate representation before converting to store.Point.
type flattenedPoint struct {
	setNumber    int
	gameNumber   int
	pointNumber  int
	server       int
	scorer       int
	homePoints   string
	awayPoints   string
	homeGames    int
	awayGames    int
	isBreakPoint bool
	isMatchPoint bool
	isSetPoint   bool
}

// deriveScorer determines who won the point by comparing consecutive scores.
// For the last point in a game, scorer = game winner.
func deriveScorer(prevHome, prevAway, curHome, curAway string, gameWinner int, isLast bool) int {
	if isLast {
		return gameWinner
	}
	// Compare point values: 0 < 15 < 30 < 40 < A
	prevH := pointValue(prevHome)
	prevA := pointValue(prevAway)
	curH := pointValue(curHome)
	curA := pointValue(curAway)

	if curH > prevH {
		return 1 // home scored
	}
	if curA > prevA {
		return 2 // away scored
	}
	// Deuce/advantage transitions: if home went from A to 40, away scored
	if prevH > curH {
		return 2
	}
	if prevA > curA {
		return 1
	}
	return gameWinner
}

// pointValue maps tennis point strings to comparable integers.
func pointValue(s string) int {
	switch strings.TrimSpace(s) {
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

func parseInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
