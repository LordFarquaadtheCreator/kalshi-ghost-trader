package httpapi

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// sseEvent is a single SSE event in the ring buffer.
type sseEvent struct {
	ID    int
	Topic string
	Data  string
	TS    time.Time
}

// sseHub manages SSE client connections and a ring buffer for resume.
type sseHub struct {
	mu     sync.Mutex
	buf    []sseEvent
	nextID int
	subs   map[chan sseEvent]struct{}
}

const ringBufferSize = 1024

func newSSEHub() *sseHub {
	return &sseHub{
		buf:  make([]sseEvent, 0, ringBufferSize),
		subs: make(map[chan sseEvent]struct{}),
	}
}

// Publish adds an event to the ring buffer and broadcasts to all subscribers.
func (h *sseHub) Publish(topic, data string) {
	h.mu.Lock()
	evt := sseEvent{
		ID:    h.nextID,
		Topic: topic,
		Data:  data,
		TS:    time.Now(),
	}
	h.nextID++

	// Ring buffer: drop oldest when full.
	if len(h.buf) >= ringBufferSize {
		h.buf = h.buf[1:]
	}
	h.buf = append(h.buf, evt)

	// Broadcast to subscribers.
	for ch := range h.subs {
		select {
		case ch <- evt:
		default:
			// Drop if subscriber can't keep up.
		}
	}
	h.mu.Unlock()
}

// handleStream handles SSE connections with topic filtering and resume.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	topics := r.URL.Query().Get("topics")
	topicSet := make(map[string]bool)
	if topics != "" {
		for _, t := range splitComma(topics) {
			topicSet[t] = true
		}
	}

	lastEventID := r.Header.Get("Last-Event-ID")
	resumeFrom := 0
	if lastEventID != "" {
		if n, err := parseInt(lastEventID); err == nil {
			resumeFrom = n
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send buffered events from resume point.
	s.sse.mu.Lock()
	for _, evt := range s.sse.buf {
		if evt.ID <= resumeFrom {
			continue
		}
		if len(topicSet) > 0 && !topicSet[evt.Topic] {
			continue
		}
		_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evt.ID, evt.Topic, evt.Data)
	}

	// Subscribe to new events.
	ch := make(chan sseEvent, 64)
	s.sse.subs[ch] = struct{}{}
	s.sse.mu.Unlock()

	defer func() {
		s.sse.mu.Lock()
		delete(s.sse.subs, ch)
		s.sse.mu.Unlock()
		close(ch)
	}()

	flusher.Flush()

	// Stream new events.
	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			if len(topicSet) > 0 && !topicSet[evt.Topic] {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evt.ID, evt.Topic, evt.Data)
			flusher.Flush()
		}
	}
}

func splitComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	return result
}
