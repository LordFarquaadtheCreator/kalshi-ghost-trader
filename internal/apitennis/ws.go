package apitennis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// WSClient manages the API-Tennis WebSocket connection with auto-reconnect.
// The server pushes match updates as JSON messages whenever a live match
// changes state (point won, game/set completed, etc).
type WSClient struct {
	url    string
	log    *slog.Logger
	minBackoff time.Duration
	maxBackoff time.Duration
}

// NewWSClient creates a WebSocket client for wss://wss.api-tennis.com/live.
func NewWSClient(apiKey, timezone string, log *slog.Logger) *WSClient {
	if timezone == "" {
		timezone = "+00:00"
	}
	url := fmt.Sprintf("wss://wss.api-tennis.com/live?APIkey=%s&timezone=%s", apiKey, timezone)
	return &WSClient{
		url:        url,
		log:        log,
		minBackoff: 1 * time.Second,
		maxBackoff: 30 * time.Second,
	}
}

// Connect dials the WebSocket server.
func (w *WSClient) Connect(ctx context.Context) (*websocket.Conn, error) {
	conn, _, err := websocket.Dial(ctx, w.url, &websocket.DialOptions{
		HTTPHeader: http.Header{},
	})
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}
	return conn, nil
}

// ReadMessage reads one JSON message from the WS and decodes it.
// Returns the raw event(s). API-Tennis may send a single object or an array.
func (w *WSClient) ReadMessage(ctx context.Context, conn *websocket.Conn) ([]WSEvent, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("ws read: %w", err)
	}

	// Try array first
	var events []WSEvent
	if err := json.Unmarshal(data, &events); err == nil && len(events) > 0 {
		return events, nil
	}

	// Try single object
	var ev WSEvent
	if err := json.Unmarshal(data, &ev); err == nil && ev.EventKey > 0 {
		return []WSEvent{ev}, nil
	}

	// Empty or unparseable — skip
	return nil, nil
}

// Backoff returns the next backoff duration, capped at maxBackoff.
func (w *WSClient) Backoff(attempt int) time.Duration {
	d := w.minBackoff << uint(attempt)
	if d > w.maxBackoff {
		d = w.maxBackoff
	}
	return d
}
