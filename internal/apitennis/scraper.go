package apitennis

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
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
	strategy   algorithms.Strategy
	tickWriter *store.TickWriter
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
// from the read loop and registers markets — all without blocking the WS read loop.
type matchWorker struct {
	eventTicker string
	eventKey    int
	ch          chan WSEvent
	done        chan struct{}
	strategy    algorithms.Strategy
	scoreObs    algorithms.ScoreObserver
	tickWriter  *store.TickWriter
	log         *slog.Logger

	// Track which points we've already processed to avoid duplicates
	seenPoints map[string]bool
}

// New creates an API-Tennis scraper. apiKey and timezone are read from config.Cfg.
func New(db *store.DB, strat algorithms.Strategy, tickWriter *store.TickWriter, log *slog.Logger) *Scraper {
	return &Scraper{
		ws:          NewWSClient(config.Cfg.APITennisAPIKey, config.Cfg.APITennisTimezone, log),
		db:          db,
		strategy:    strat,
		tickWriter:  tickWriter,
		log:         log,
		workers:     make(map[string]*matchWorker),
		matchCache:  make(map[int]string),
		marketCache: make(map[string][]string),
	}
}

// StartPolling is called by the tracker when an event's markets are subscribed.
// Creates a per-match worker goroutine.
func (s *Scraper) StartPolling(eventTicker string) {
	w := &matchWorker{
		eventTicker: eventTicker,
		ch:          make(chan WSEvent, 64),
		done:        make(chan struct{}),
		strategy:    s.strategy,
		tickWriter:  s.tickWriter,
		seenPoints:  make(map[string]bool),
		log:         s.log,
	}
	if obs, ok := s.strategy.(algorithms.ScoreObserver); ok {
		w.scoreObs = obs
	}

	s.workersMu.Lock()
	s.workers[eventTicker] = w
	s.workersMu.Unlock()

	go w.run()

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

// run is the per-match worker goroutine. Converts WSEvents to store.Point,
// dispatches OnPoint to strategies, and ingests into TickWriter for DB storage.
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

// processEvent extracts new points from a WSEvent and dispatches them.
// API-Tennis sends full point-by-point data on every push — dedup via seenPoints.
func (w *matchWorker) processEvent(ev WSEvent) {
	now := time.Now().UnixMilli()

	// Accumulate set games from Scores
	setGamesHome := make(map[int]int)
	setGamesAway := make(map[int]int)
	for _, sc := range ev.Scores {
		sn := parseSetNumber(sc.ScoreSet)
		setGamesHome[sn] = atoiSafe(sc.ScoreFirst)
		setGamesAway[sn] = atoiSafe(sc.ScoreSecond)
	}

	for _, setData := range ev.PointByPoint {
		setNum := parseSetNumber(setData.SetNumber)
		gameNum := atoiSafe(setData.NumberGame)
		server := parseServer(setData.PlayerServed)
		scorer := parseServer(setData.ServeWinner)
		if scorer == 0 && setData.ServeLost != "" {
			scorer = parseServer(setData.ServeLost)
		}

		// Games before this set
		homeSetGames := 0
		awaySetGames := 0
		for sn := 1; sn < setNum; sn++ {
			homeSetGames += setGamesHome[sn]
			awaySetGames += setGamesAway[sn]
		}

		// Current game score from SetData.Score "h - a"
		homeGames, awayGames := parseScore(setData.Score)
		hg := atoiSafe(homeGames)
		ag := atoiSafe(awayGames)

		isTB := false
		if hg == 6 && ag == 6 {
			isTB = true
		}

		for _, pt := range setData.Points {
			ptNum := atoiSafe(pt.NumberPoint)
			ptKey := strconv.Itoa(setNum) + ":" + strconv.Itoa(gameNum) + ":" + strconv.Itoa(ptNum)
			if w.seenPoints[ptKey] {
				continue
			}
			w.seenPoints[ptKey] = true

			homePts, awayPts := parseScore(pt.Score)

			p := store.Point{
				MatchTicker:  w.eventTicker,
				FSMatchID:    strconv.Itoa(ev.EventKey),
				TS:           now,
				RecvTS:       now,
				SetNumber:    setNum,
				GameNumber:   gameNum,
				PointNumber:  ptNum,
				Server:       server,
				Scorer:       scorer,
				HomePoints:   homePts,
				AwayPoints:   awayPts,
				HomeGames:    hg,
				AwayGames:    ag,
				HomeSetGames: homeSetGames,
				AwaySetGames: awaySetGames,
				IsTiebreak:   isTB,
			}

			// Classify point flags
			pc := algorithms.ClassifyPoint(algorithms.PointContext{
				SetsHome:   homeSetGames,
				SetsAway:   awaySetGames,
				HomeGames:  hg,
				AwayGames:  ag,
				HomePoints: homePts,
				AwayPoints: awayPts,
				Server:     server,
				IsTiebreak: isTB,
			})
			p.IsBreakPoint = pc.IsBreakPoint
			p.IsSetPoint = pc.IsSetPoint
			p.IsMatchPoint = pc.IsMatchPoint

			// Store to DB
			if w.tickWriter != nil {
				w.tickWriter.IngestPoint(p)
			}

			// Dispatch to strategies
			if w.scoreObs != nil {
				w.scoreObs.OnPoint(w.eventTicker, p)
			}
		}
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
