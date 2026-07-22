package httpapi

import (
	"encoding/json"
	"net/http"
)

// cors wraps a handler with CORS headers for dashboard cross-origin calls.
func (s *Server) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// handleTracked returns currently tracked markets + event/market counts.
// Matches v1 dashboard shape: {subs, event_count, market_count, scores, latest_tick_ts}.
func (s *Server) handleTracked(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=2")

	if s.tracker == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"subs":         []any{},
			"event_count":  0,
			"market_count": 0,
			"scores":       map[string]any{},
		})
		return
	}

	subs := s.tracker.ActiveSubs()
	events := s.tracker.ActiveEvents()

	// Enrich with event titles + occurrence_ts from DB.
	if s.db != nil {
		titles, occTS := s.fetchEventMeta(r, events)
		for i := range subs {
			subs[i].Title = titles[subs[i].EventTicker]
			subs[i].OccurrenceTS = occTS[subs[i].EventTicker]
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"subs":         subs,
		"event_count":  len(events),
		"market_count": len(subs),
		"scores":       map[string]any{},
	})
}

// handleOrderCounts returns sim order counts per event.
func (s *Server) handleOrderCounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	if !s.checkDB(w) {
		return
	}

	type countRow struct {
		EventTicker string `gorm:"column:event_ticker"`
		Count       int64  `gorm:"column:cnt"`
	}
	var rows []countRow
	s.db.Raw(`SELECT event_ticker, count(*) AS cnt FROM orders_v2 GROUP BY event_ticker`).Scan(&rows)

	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.EventTicker] = int(row.Count)
	}

	writeJSON(w, http.StatusOK, map[string]any{"counts": counts})
}

// handlePendingOrderCounts returns pending (non-terminal) order counts per event.
func (s *Server) handlePendingOrderCounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	if !s.checkDB(w) {
		return
	}

	type countRow struct {
		EventTicker string `gorm:"column:event_ticker"`
		Count       int64  `gorm:"column:cnt"`
	}
	var rows []countRow
	s.db.Raw(`SELECT event_ticker, count(*) AS cnt FROM orders_v2
		WHERE status IN ('submitted','held','partial','unverified') GROUP BY event_ticker`).Scan(&rows)

	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.EventTicker] = int(row.Count)
	}

	writeJSON(w, http.StatusOK, map[string]any{"counts": counts})
}

// fetchEventMeta bulk-fetches event titles + occurrence_ts for the tracked events.
func (s *Server) fetchEventMeta(r *http.Request, events []string) (titles map[string]string, occTS map[string]int64) {
	titles = make(map[string]string)
	occTS = make(map[string]int64)
	if len(events) == 0 {
		return
	}

	type metaRow struct {
		EventTicker string `gorm:"column:event_ticker"`
		Title       string `gorm:"column:title"`
		OccurrenceTS int64 `gorm:"column:occurrence_ts"`
	}
	var rows []metaRow
	// Use first market's occurrence_ts as event occurrence.
	s.db.WithContext(r.Context()).Raw(`
		SELECT DISTINCT ON (e.event_ticker)
			e.event_ticker, e.title, m.occurrence_ts
		FROM events e
		JOIN markets m ON m.event_ticker = e.event_ticker
		WHERE e.event_ticker IN ?`, events).Scan(&rows)

	for _, row := range rows {
		titles[row.EventTicker] = row.Title
		occTS[row.EventTicker] = row.OccurrenceTS
	}
	return
}

// ensure json import is used
var _ = json.Marshal
