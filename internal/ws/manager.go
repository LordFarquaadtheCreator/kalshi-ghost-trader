// Package ws implements a WebSocket manager for the Kalshi real-time market data feed.
//
// The Manager maintains a single multiplexed connection to Kalshi's WebSocket API,
// with automatic reconnection using exponential backoff with jitter. On reconnect,
// all active subscriptions are replayed per-market so each retains its own
// server-assigned subscription IDs (sids) for clean unsubscribe.
//
// Subscribed channels:
//   - ticker — market price updates (filtered by market_tickers)
//   - trade — public trade fills (filtered by market_tickers)
//   - orderbook_delta — orderbook depth changes (filtered by market_tickers;
//     server sends orderbook_snapshot first, then incremental deltas)
//   - market_lifecycle_v2 — market status changes (NOT filterable; client-side
//     filter via subs map; also delivers event_lifecycle and event_fee_update)
//
// Incoming messages are parsed in the read loop and dispatched to handlers that
// ingest data into store.TickWriter. Lifecycle timestamps from the WS feed are
// in seconds and are converted to milliseconds before storage.
//
// The coder/websocket library auto-responds to ping frames during Read, so no
// manual pong handling is needed. Kalshi sends a ping every 10 seconds.
package ws

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiauth"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

// subInfo tracks a per-market subscription: server-assigned subscription
// IDs (sids) for unsubscribe, and ack coordination for Subscribe.
type subInfo struct {
	sids   []int64 // set when "subscribed" acks arrive
	acked  chan struct{}
	ackErr error
}

// PriceUpdater receives market price updates from WS ticker messages.
// Implemented by algorithms.MatchPointStrategy to track live prices for edge calculation.
type PriceUpdater interface {
	OnPrice(marketTicker string, price float64)
}

// Manager owns the single multiplexed Kalshi WebSocket connection.
// Auto-reconnects with exponential backoff and replays subscriptions.
type Manager struct {
	wsURL  string
	signer *kalshiauth.Signer
	log    *slog.Logger

	minBackoff time.Duration
	maxBackoff time.Duration

	// seriesFilter restricts event_lifecycle storage to configured tennis series.
	// The lifecycle channel is unfiltered — without this, all Kalshi events get stored.
	seriesFilter map[string]bool

	mu          sync.Mutex
	cmdMu       sync.Mutex // protects cmdToMarket (written by sendSubscribeConn, read by handleSubscribed)
	conn        *websocket.Conn
	subs        map[string]*subInfo // market_ticker -> subscription info
	cmdToMarket map[int64]string    // command id -> market (for sid mapping)
	msgID       atomic.Int64

	// Seq gap tracking: sid -> last seen seq. Cleared on reconnect.
	seqMu   sync.Mutex
	lastSeq map[int64]int64
	SeqGaps atomic.Int64 // total missed messages across all sids

	// Latency tracking: recv_ts - server ts_ms for ticker/trade messages.
	latencyMu    sync.Mutex
	latencySum   int64 // ms
	latencyCount int64
	latencyMax   int64 // ms

	tickWriter  *store.TickWriter
	priceUpd    PriceUpdater // nil if no signal generator
	disableSave bool         // skip persisting WS data to DB
}

// NewManager creates a WebSocket manager. series filters which event_lifecycle
// messages get stored (lifecycle channel is unfiltered server-side).
// disableSave skips all WS data persistence to DB.
func NewManager(wsURL string, signer *kalshiauth.Signer, tw *store.TickWriter, series []string, minBackoff, maxBackoff time.Duration, disableSave bool, log *slog.Logger) *Manager {
	sf := make(map[string]bool, len(series))
	for _, s := range series {
		sf[s] = true
	}
	return &Manager{
		wsURL:        wsURL,
		signer:       signer,
		log:          log,
		minBackoff:   minBackoff,
		maxBackoff:   maxBackoff,
		seriesFilter: sf,
		subs:         make(map[string]*subInfo),
		cmdToMarket:  make(map[int64]string),
		lastSeq:      make(map[int64]int64),
		tickWriter:   tw,
		disableSave:  disableSave,
	}
}

// SetPriceUpdater wires a price tracker (algorithms.MatchPointStrategy) to receive
// market price updates from WS ticker messages.
func (m *Manager) SetPriceUpdater(pu PriceUpdater) {
	m.priceUpd = pu
}

// Run maintains the connection until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	backoff := m.minBackoff

	for {
		if ctx.Err() != nil {
			m.clearSubs()
			return ctx.Err()
		}

		conn, err := m.dial(ctx)
		if err != nil {
			m.log.Warn("ws dial failed", "err", err, "backoff", backoff)
			if waitErr := m.sleep(ctx, backoff); waitErr != nil {
				m.clearSubs()
				return waitErr
			}
			backoff = m.nextBackoff(backoff)
			continue
		}
		backoff = m.minBackoff
		m.log.Info("ws connected")

		m.mu.Lock()
		m.conn = conn
		m.mu.Unlock()
		// Clear stale command mapping — old ids are invalid on new connection
		m.cmdMu.Lock()
		m.cmdToMarket = make(map[int64]string)
		m.cmdMu.Unlock()

		// Clear seq tracking — new connection resets all sids
		m.seqMu.Lock()
		m.lastSeq = make(map[int64]int64)
		m.seqMu.Unlock()

		// Replay subscriptions after (re)connect
		if err := m.replaySubscriptions(ctx); err != nil {
			m.log.Warn("resubscribe failed", "err", err)
			m.dropConn()
			continue
		}

		// Read loop — returns on error/close
		readErr := m.readLoop(ctx, conn)
		m.dropConn()
		m.log.Info("ws read loop ended", "err", readErr)

		if ctx.Err() != nil {
			m.clearSubs()
			return ctx.Err()
		}

		// Brief pause before reconnect
		if waitErr := m.sleep(ctx, m.minBackoff); waitErr != nil {
			m.clearSubs()
			return waitErr
		}
	}
}

func (m *Manager) dial(ctx context.Context) (*websocket.Conn, error) {
	headers, err := m.signer.WSHeaders()
	if err != nil {
		return nil, fmt.Errorf("sign ws headers: %w", err)
	}

	httpHeaders := http.Header{}
	for k, v := range headers {
		httpHeaders.Set(k, v)
	}

	conn, _, err := websocket.Dial(ctx, m.wsURL, &websocket.DialOptions{
		HTTPHeader: httpHeaders,
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(wsReadLimit)
	return conn, nil
}

func (m *Manager) dropConn() {
	m.mu.Lock()
	if m.conn != nil {
		m.conn.Close(websocket.StatusNormalClosure, "")
		m.conn = nil
	}
	m.mu.Unlock()
}

func (m *Manager) clearSubs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = make(map[string]*subInfo)
}

func (m *Manager) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) nextBackoff(b time.Duration) time.Duration {
	b *= 2
	if b > m.maxBackoff {
		b = m.maxBackoff
	}
	// Add jitter: +0 to +25% of backoff
	jitter := time.Duration(rand.Int63n(int64(b/4) + 1))
	return b + jitter
}
