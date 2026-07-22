package httpapi

import (
	"encoding/json"
	"net/http"
)

// checkDB returns true if DB is available, false + writes problem if not.
func (s *Server) checkDB(w http.ResponseWriter) bool {
	if s.db == nil {
		writeProblem(w, http.StatusServiceUnavailable, "no_db", "Database not configured")
		return false
	}
	return true
}

// handleOverview returns equity curve, open exposure, today's funnel.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	// Query pool_equity_curve for last 90 days.
	type equityPoint struct {
		Day             string `json:"day"`
		DeltaCents      int64  `json:"delta_cents"`
		CumulativeCents int64  `json:"cumulative_cents"`
	}
	var equity []equityPoint
	s.db.Raw(`
		SELECT day::text, delta_cents, cumulative_cents
		FROM insights.pool_equity_curve
		WHERE day >= CURRENT_DATE - INTERVAL '90 days'
		ORDER BY day
	`).Scan(&equity)

	// Today's funnel.
	type funnel struct {
		Intents   int64 `json:"intents"`
		Gated     int64 `json:"gated"`
		Submitted int64 `json:"submitted"`
		Filled    int64 `json:"filled"`
	}
	var f funnel
	s.db.Raw(`
		SELECT
			count(*) AS intents,
			count(*) FILTER (WHERE status = 'gated') AS gated,
			count(*) FILTER (WHERE status IN ('submitted', 'held')) AS submitted,
			count(*) FILTER (WHERE status IN ('filled', 'partial')) AS filled
		FROM orders_v2
		WHERE ts_intent >= extract(epoch from CURRENT_DATE) * 1000
	`).Scan(&f)

	// Open exposure.
	var openExposure struct {
		Count   int   `json:"count"`
		Cents   int64 `json:"cents"`
	}
	s.db.Raw(`
		SELECT count(*) AS count, COALESCE(sum(contracts * price_cents), 0) AS cents
		FROM orders_v2
		WHERE status IN ('submitted', 'held', 'partial')
	`).Scan(&openExposure)

	writeJSON(w, http.StatusOK, map[string]any{
		"equity_curve":   equity,
		"open_exposure":  openExposure,
		"today_funnel":   f,
	})
}

// handleStrategies returns strategy_daily aggregated, sortable.
func (s *Server) handleStrategies(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "realized_pnl_cents"
	}

	// Whitelist sort columns.
	validSorts := map[string]bool{
		"realized_pnl_cents": true,
		"win_rate":           true,
		"filled_count":       true,
		"total_intents":      true,
		"invested_cents":     true,
	}
	if !validSorts[sortBy] {
		sortBy = "realized_pnl_cents"
	}

	type strategyRow struct {
		Strategy        string  `json:"strategy"`
		GatedCount      int64   `json:"gated_count"`
		FilledCount     int64   `json:"filled_count"`
		WonCount        int64   `json:"won_count"`
		LostCount       int64   `json:"lost_count"`
		RealizedPnlCents int64  `json:"realized_pnl_cents"`
		WinRate         *float64 `json:"win_rate"`
		TotalIntents    int64   `json:"total_intents"`
	}
	var rows []strategyRow
	s.db.Raw(`
		SELECT strategy,
			sum(gated_count) AS gated_count,
			sum(filled_count) AS filled_count,
			sum(won_count) AS won_count,
			sum(lost_count) AS lost_count,
			sum(realized_pnl_cents) AS realized_pnl_cents,
			CASE WHEN sum(filled_count) > 0
				THEN sum(won_count)::numeric / sum(filled_count)
				ELSE NULL END AS win_rate,
			sum(total_intents) AS total_intents
		FROM insights.strategy_daily
		GROUP BY strategy
		ORDER BY `+sortBy+` DESC
	`).Scan(&rows)

	writeJSON(w, http.StatusOK, rows)
}

// handleStrategyDetail returns daily series + band_performance rows.
func (s *Server) handleStrategyDetail(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeProblem(w, http.StatusBadRequest, "missing_name", "Strategy name required")
		return
	}

	type dailyRow struct {
		Day              string  `json:"day"`
		GateReason       *string `json:"gate_reason"`
		GatedCount       int64   `json:"gated_count"`
		FilledCount      int64   `json:"filled_count"`
		RealizedPnlCents int64   `json:"realized_pnl_cents"`
	}
	var daily []dailyRow
	s.db.Raw(`
		SELECT day::text, gate_reason, gated_count, filled_count, realized_pnl_cents
		FROM insights.strategy_daily
		WHERE strategy = ?
		ORDER BY day DESC
	`, name).Scan(&daily)

	type bandRow struct {
		BandCents     int     `json:"band_cents"`
		Fills         int64   `json:"fills"`
		HitRate       *float64 `json:"hit_rate"`
		PnlCents      int64   `json:"pnl_cents"`
		InvestedCents int64   `json:"invested_cents"`
	}
	var bands []bandRow
	s.db.Raw(`
		SELECT band_cents, fills, hit_rate, pnl_cents, invested_cents
		FROM insights.band_performance
		WHERE strategy = ?
		ORDER BY band_cents
	`, name).Scan(&bands)

	writeJSON(w, http.StatusOK, map[string]any{
		"daily": daily,
		"bands": bands,
	})
}

// handleMatches returns match_summary + live loop state.
func (s *Server) handleMatches(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	state := r.URL.Query().Get("state")

	var matches []map[string]any
	if state == "live" {
		// Live matches — those with orders in the last hour.
		s.db.Raw(`
			SELECT DISTINCT event_ticker, total_orders, filled_orders, realized_pnl_cents
			FROM insights.match_summary
			WHERE event_ticker IN (
				SELECT DISTINCT event_ticker FROM orders_v2
				WHERE ts_intent >= extract(epoch from now()) * 1000 - 3600000
			)
			ORDER BY event_ticker
		`).Scan(&matches)
	} else {
		s.db.Raw(`
			SELECT event_ticker, total_orders, filled_orders, gated_orders, realized_pnl_cents
			FROM insights.match_summary
			ORDER BY event_ticker
		`).Scan(&matches)
	}

	writeJSON(w, http.StatusOK, matches)
}

// handleMatchDetail returns markets, orders, gated signals for a match.
func (s *Server) handleMatchDetail(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	event := r.PathValue("event")
	if event == "" {
		writeProblem(w, http.StatusBadRequest, "missing_event", "Event ticker required")
		return
	}

	var orders []map[string]any
	s.db.Raw(`
		SELECT id, market_ticker, strategy, action, contracts, price_cents,
			status, gate_reason, fill_count, fill_price_cents,
			ts_intent, ts_submitted, ts_acked
		FROM orders_v2
		WHERE event_ticker = ?
		ORDER BY ts_intent DESC
		LIMIT 200
	`, event).Scan(&orders)

	writeJSON(w, http.StatusOK, map[string]any{
		"event_ticker": event,
		"orders":       orders,
	})
}

// handleOrders returns cursor-paginated orders.
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	cursor, limit := parseCursor(r)
	status := r.URL.Query().Get("status")

	query := `SELECT id, client_order_id, event_ticker, market_ticker, strategy,
		action, contracts, price_cents, status, gate_reason, fill_count,
		fill_price_cents, ts_intent, ts_submitted, ts_acked
		FROM orders_v2`
	args := []any{}
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	if cursor != "" {
		if status != "" {
			query += " AND"
		} else {
			query += " WHERE"
		}
		query += " (ts_intent, id) < (?, ?)"
		// Parse cursor "ts,id".
		var ts int64
		var id int64
		if _, err := parseCursorPair(cursor, &ts, &id); err == nil {
			args = append(args, ts, id)
		}
	}
	query += " ORDER BY ts_intent DESC, id DESC LIMIT ?"
	args = append(args, limit+1)

	var orders []map[string]any
	s.db.Raw(query, args...).Scan(&orders)

	var nextCursor string
	if len(orders) > limit {
		last := orders[limit-1]
		nextCursor = buildCursor(last["ts_intent"], last["id"])
		orders = orders[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"orders": orders,
		"cursor": nextCursor,
	})
}

// handleLedger returns cursor-paginated ledger entries.
func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	cursor, limit := parseCursor(r)

	query := `SELECT id, ts, entry_type, amount_cents, order_id, note
		FROM pool_ledger`
	args := []any{}
	if cursor != "" {
		var ts int64
		var id int64
		if _, err := parseCursorPair(cursor, &ts, &id); err == nil {
			query += " WHERE (ts, id) < (?, ?)"
			args = append(args, ts, id)
		}
	}
	query += " ORDER BY ts DESC, id DESC LIMIT ?"
	args = append(args, limit+1)

	var entries []map[string]any
	s.db.Raw(query, args...).Scan(&entries)

	var nextCursor string
	if len(entries) > limit {
		last := entries[limit-1]
		nextCursor = buildCursor(last["ts"], last["id"])
		entries = entries[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"cursor":  nextCursor,
	})
}

// handleGetConfig returns config keys with value + classification + last_applied_ts.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	var configs []map[string]any
	s.db.Raw(`
		SELECT key, value, classification, updated_ts
		FROM app_config
		ORDER BY key
	`).Scan(&configs)

	writeJSON(w, http.StatusOK, configs)
}

// handlePutConfig updates a config key.
func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if body.Key == "" {
		writeProblem(w, http.StatusBadRequest, "missing_key", "Config key required")
		return
	}

	result := s.db.Exec(`
		UPDATE app_config SET value = ?, updated_ts = extract(epoch from now()) * 1000
		WHERE key = ?
	`, body.Value, body.Key)
	if result.Error != nil {
		writeProblem(w, http.StatusInternalServerError, "db_error", result.Error.Error())
		return
	}
	if result.RowsAffected == 0 {
		writeProblem(w, http.StatusNotFound, "not_found", "Config key not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"key": body.Key, "updated": true})
}

// parseCursorPair parses "ts,id" cursor format.
func parseCursorPair(s string, ts, id *int64) (int, error) {
	return parseCursorPairImpl(s, ts, id)
}
