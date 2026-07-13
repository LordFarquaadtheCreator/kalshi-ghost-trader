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
	sids    []int64 // set when "subscribed" acks arrive
	acked   chan struct{}
	ackErr  error
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

	tickWriter *store.TickWriter
}

// NewManager creates a WebSocket manager. series filters which event_lifecycle
// messages get stored (lifecycle channel is unfiltered server-side).
func NewManager(wsURL string, signer *kalshiauth.Signer, tw *store.TickWriter, series []string, minBackoff, maxBackoff time.Duration, log *slog.Logger) *Manager {
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
		tickWriter:   tw,
	}
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
