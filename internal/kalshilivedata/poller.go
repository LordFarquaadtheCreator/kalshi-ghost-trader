// Package kalshilivedata implements a backup live score poller using
// Kalshi's /milestones and /live_data endpoints.
//
// When API-Tennis has no data for a match (ITF Futures, Davis Cup rubbers,
// exhibition matches), this poller fills the gap by polling Kalshi's own
// live-data API every N seconds. Score snapshots are stored in the
// kalshi_scores table and dispatched to strategies via ScoreObserver.
//
// API-Tennis remains the primary score source. This poller only dispatches
// OnPoint to strategies when API-Tennis has no points for the match.
// Storage and dashboard display happen regardless (backup data).
package kalshilivedata

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/algorithms"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// Poller polls Kalshi's live-data API for score updates on tracked matches.
// Implements tracker.ScorePoller for lifecycle integration.
type Poller struct {
	client       *kalshiclient.Client
	db           *store.DB
	strategy     algorithms.Strategy
	tickWriter   *store.TickWriter
	log          *slog.Logger
	pollInterval time.Duration

	mu      sync.Mutex
	workers map[string]*matchPoller // event_ticker → poller
}

// matchPoller runs one goroutine per active match.
type matchPoller struct {
	eventTicker string
	client      *kalshiclient.Client
	db          *store.DB
	strategy    algorithms.Strategy
	scoreObs    algorithms.ScoreObserver
	tickWriter  *store.TickWriter
	log         *slog.Logger
	interval    time.Duration
	done        chan struct{}

	milestoneID string

	// Last score state — only dispatch OnPoint on change.
	lastPointsHome int
	lastPointsAway int
	lastGamesHome  int
	lastGamesAway  int
	lastSetsHome   int
	lastSetsAway   int
}

// New creates a Kalshi live-data poller. pollInterval is read from config.Cfg.KalshiLiveDataPollSecs.
func New(client *kalshiclient.Client, db *store.DB, strat algorithms.Strategy,
	tw *store.TickWriter, log *slog.Logger) *Poller {
	pollInterval := time.Duration(config.Cfg.KalshiLiveDataPollSecs) * time.Second
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Poller{
		client:       client,
		db:           db,
		strategy:     strat,
		tickWriter:   tw,
		log:          log,
		pollInterval: pollInterval,
		workers:      make(map[string]*matchPoller),
	}
}

// StartPolling is called by the tracker when an event's markets are subscribed.
func (p *Poller) StartPolling(eventTicker string) {
	w := &matchPoller{
		eventTicker: eventTicker,
		client:      p.client,
		db:          p.db,
		strategy:    p.strategy,
		tickWriter:  p.tickWriter,
		log:         p.log,
		interval:    p.pollInterval,
		done:        make(chan struct{}),
	}
	if obs, ok := p.strategy.(algorithms.ScoreObserver); ok {
		w.scoreObs = obs
	}

	p.mu.Lock()
	p.workers[eventTicker] = w
	p.mu.Unlock()

	go w.run()

	p.log.Info("kalshi livedata poller started", "event", eventTicker)
}

// StopPolling is called by the tracker when an event's markets are unsubscribed.
func (p *Poller) StopPolling(eventTicker string) {
	p.mu.Lock()
	w, ok := p.workers[eventTicker]
	if ok {
		delete(p.workers, eventTicker)
	}
	p.mu.Unlock()

	if ok {
		close(w.done)
	}

	if p.strategy != nil {
		p.strategy.UnregisterMarkets(eventTicker)
	}
	p.log.Info("kalshi livedata poller stopped", "event", eventTicker)
}

// Run blocks until ctx cancelled. Per-match goroutines are launched by
// StartPolling via tracker; this just ensures clean shutdown.
func (p *Poller) Run(ctx context.Context) error {
	<-ctx.Done()
	p.stopAll()
	return ctx.Err()
}

func (p *Poller) stopAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		close(w.done)
	}
	p.workers = make(map[string]*matchPoller)
}

// run is the per-match poll loop. Resolves the milestone ID once, then polls
// live_data at the configured interval until done or milestone disappears.
func (w *matchPoller) run() {
	if !w.resolveMilestone() {
		return
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			if w.poll() {
				return
			}
		}
	}
}

// resolveMilestone fetches the milestone ID for this event. Retries with
// backoff until success or done.
func (w *matchPoller) resolveMilestone() bool {
	backoff := 5 * time.Second
	for {
		select {
		case <-w.done:
			return false
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		milestones, err := w.client.GetMilestones(ctx, w.eventTicker)
		cancel()
		if err != nil {
			w.log.Warn("kalshi livedata: get milestones", "event", w.eventTicker, "err", err)
			select {
			case <-w.done:
				return false
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		if len(milestones) == 0 {
			select {
			case <-w.done:
				return false
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		w.milestoneID = milestones[0].ID
		w.log.Info("kalshi livedata: milestone resolved", "event", w.eventTicker,
			"milestone_id", w.milestoneID, "type", milestones[0].Type)
		return true
	}
}

// poll fetches one live_data snapshot, stores it, and dispatches to strategies
// if the score changed and API-Tennis has no data for this match.
// Returns true if the milestone is gone (404) and the poller should stop.
func (w *matchPoller) poll() (stop bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ld, err := w.client.GetLiveData(ctx, w.milestoneID)
	if err != nil {
		var apiErr kalshiclient.APIError
		if errors.As(err, &apiErr) && apiErr.Code == 404 {
			w.log.Info("kalshi livedata: milestone gone, stopping poller",
				"event", w.eventTicker, "milestone_id", w.milestoneID)
			return true
		}
		w.log.Warn("kalshi livedata: get live data", "event", w.eventTicker, "err", err)
		return false
	}

	d := &ld.Details

	// Determine server: competitor1_id → 1 (home), competitor2_id → 2 (away).
	server := 0
	if d.Server != "" {
		if d.Server == d.Competitor1ID {
			server = 1
		} else if d.Server == d.Competitor2ID {
			server = 2
		}
	}

	// Current set = completed_rounds + 1 (1-based).
	currentSet := d.CompletedRounds + 1

	// Games in current set come from round_scores[completed_rounds].
	// Points in current game come from current_round_score.
	gamesHome, gamesAway := 0, 0
	if d.CompletedRounds < len(d.Competitor1RoundScores) {
		gamesHome = d.Competitor1RoundScores[d.CompletedRounds].Score
	}
	if d.CompletedRounds < len(d.Competitor2RoundScores) {
		gamesAway = d.Competitor2RoundScores[d.CompletedRounds].Score
	}

	pointsHome := d.Competitor1CurrentRoundScore
	pointsAway := d.Competitor2CurrentRoundScore

	payload, _ := json.Marshal(ld)

	score := store.KalshiScore{
		EventTicker:     w.eventTicker,
		MilestoneID:     w.milestoneID,
		Status:          d.Status,
		SetsHome:        d.Competitor1OverallScore,
		SetsAway:        d.Competitor2OverallScore,
		GamesHome:       gamesHome,
		GamesAway:       gamesAway,
		PointsHome:      pointsHome,
		PointsAway:      pointsAway,
		Server:          server,
		CompletedRounds: d.CompletedRounds,
		UpdatedTS:       time.Now().UnixMilli(),
		Payload:         string(payload),
	}

	if err := w.db.UpsertKalshiScore(ctx, score); err != nil {
		w.log.Warn("kalshi livedata: upsert score", "event", w.eventTicker, "err", err)
	}

	// Only dispatch to strategies if score changed and API-Tennis has no data.
	hasAPItennis, _ := w.db.HasAPItennisPoints(ctx, w.eventTicker)
	if hasAPItennis {
		return false
	}

	changed := pointsHome != w.lastPointsHome ||
		pointsAway != w.lastPointsAway ||
		gamesHome != w.lastGamesHome ||
		gamesAway != w.lastGamesAway ||
		d.Competitor1OverallScore != w.lastSetsHome ||
		d.Competitor2OverallScore != w.lastSetsAway

	w.lastPointsHome = pointsHome
	w.lastPointsAway = pointsAway
	w.lastGamesHome = gamesHome
	w.lastGamesAway = gamesAway
	w.lastSetsHome = d.Competitor1OverallScore
	w.lastSetsAway = d.Competitor2OverallScore

	if !changed {
		return false
	}

	p := synthesizePoint(w.eventTicker, w.milestoneID, score, currentSet)
	// Persist to points table so HasPoints works for the real order gate.
	// Without this, kalshi-only matches are invisible to match-started detection.
	if w.tickWriter != nil {
		w.tickWriter.IngestPoint(p)
	}
	if w.scoreObs != nil {
		w.scoreObs.OnPoint(w.eventTicker, p)
	}
	return false
}

// synthesizePoint creates a store.Point from a Kalshi live-data snapshot.
// Point scores converted from int (0/15/30/40/50) to string ("0"/"15"/"30"/"40"/"A").
func synthesizePoint(eventTicker, milestoneID string, s store.KalshiScore, currentSet int) store.Point {
	now := time.Now().UnixMilli()
	p := store.Point{
		MatchTicker:  eventTicker,
		FSMatchID:    "kalshi-" + milestoneID,
		TS:           now,
		RecvTS:       now,
		SetNumber:    currentSet,
		GameNumber:   s.GamesHome + s.GamesAway + 1,
		PointNumber:  0,
		Server:       s.Server,
		Scorer:       0,
		HomePoints:   pointScoreToString(s.PointsHome),
		AwayPoints:   pointScoreToString(s.PointsAway),
		HomeGames:    s.GamesHome,
		AwayGames:    s.GamesAway,
		HomeSetGames: s.SetsHome,
		AwaySetGames: s.SetsAway,
	}

	setsToWin := 2

	homeCanWinSet := canWinSet(s.GamesHome, s.GamesAway, true)
	awayCanWinSet := canWinSet(s.GamesHome, s.GamesAway, false)
	p.IsSetPoint = homeCanWinSet || awayCanWinSet

	if s.SetsHome == setsToWin-1 && homeCanWinSet {
		p.IsMatchPoint = true
	}
	if s.SetsAway == setsToWin-1 && awayCanWinSet {
		p.IsMatchPoint = true
	}

	if s.Server == 1 && awayCanWinSet {
		p.IsBreakPoint = true
	}
	if s.Server == 2 && homeCanWinSet {
		p.IsBreakPoint = true
	}

	if s.GamesHome == 6 && s.GamesAway == 6 {
		p.IsTiebreak = true
	}

	return p
}

// pointScoreToString converts Kalshi's integer point score to tennis notation.
// 0→"0", 15→"15", 30→"30", 40→"40", 50→"A" (advantage).
func pointScoreToString(n int) string {
	switch n {
	case 50:
		return "A"
	case 40, 30, 15, 0:
		return strconv.Itoa(n)
	default:
		return strconv.Itoa(n)
	}
}

// canWinSet returns true if the given player can win the current set by
// winning the current game.
func canWinSet(gamesHome, gamesAway int, home bool) bool {
	if home {
		newHome := gamesHome + 1
		if newHome >= 6 && newHome-gamesAway >= 2 {
			return true
		}
		if newHome == 7 && (gamesAway == 5 || gamesAway == 6) {
			return true
		}
		return false
	}
	newAway := gamesAway + 1
	if newAway >= 6 && newAway-gamesHome >= 2 {
		return true
	}
	if newAway == 7 && (gamesHome == 5 || gamesHome == 6) {
		return true
	}
	return false
}
