package dashboardapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"strconv"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/dashboarddata"
	"github.com/farquaad/kalshi-ghost-trader/internal/liquiditypool"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/strategyconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
	"github.com/farquaad/kalshi-ghost-trader/internal/triggerranges"
)

type Deps struct {
	Tracker   *tracker.Tracker
	Engine    *backtest.Engine
	LiveStore *dashboarddata.LiveStore
	DB        *store.DB
	Log       *slog.Logger
}

type Server struct {
	deps Deps
}

func NewServer(deps Deps) *Server {
	return &Server{deps: deps}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/api/tracked", s.trackedHandler)
	mux.HandleFunc("/api/strategies", corsHandler(s.strategyListHandler))
	mux.HandleFunc("/api/simulation", corsHandler(s.simulationHandler))
	mux.HandleFunc("/api/paper-orders-insights", corsHandler(s.paperOrdersInsightsHandler))
	mux.HandleFunc("/api/ticks", corsHandler(s.ticksHandler))
	mux.HandleFunc("/api/orders", corsHandler(s.ordersHandler))
	mux.HandleFunc("/api/order-counts", corsHandler(s.orderCountsHandler))
	mux.HandleFunc("/api/pending-order-counts", corsHandler(s.pendingOrderCountsHandler))
	mux.HandleFunc("/api/passed-matches", corsHandler(s.passedMatchesHandler))
	mux.HandleFunc("/api/real-orders", corsHandler(s.realOrdersHandler))
	mux.HandleFunc("/api/liquidity-pool", corsHandler(s.liquidityPoolHandler))
	mux.HandleFunc("/api/liquidity-pool/reset", corsHandler(s.liquidityPoolResetHandler))
	mux.HandleFunc("/api/liquidity-pool/topup", corsHandler(s.liquidityPoolTopUpHandler))
	mux.HandleFunc("/api/strategy-config", corsHandler(s.strategyConfigHandler))
	mux.HandleFunc("/api/trigger-ranges", corsHandler(s.triggerRangesHandler))
	mux.HandleFunc("/api/app-config", corsHandler(s.appConfigHandler))
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

func corsHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	resp := map[string]any{
		"goroutines":        runtime.NumGoroutine(),
		"num_cpu":           runtime.NumCPU(),
		"num_threads":       runtime.GOMAXPROCS(0),
		"heap_alloc_bytes":  m.HeapAlloc,
		"heap_sys_bytes":    m.HeapSys,
		"heap_objects":      m.HeapObjects,
		"stack_inuse_bytes": m.StackInuse,
		"sys_bytes":         m.Sys,
		"mallocs":           m.Mallocs,
		"frees":             m.Frees,
		"gc_num":            m.NumGC,
		"gc_pause_ns":       m.PauseTotalNs,
		"gc_last_ns":        m.LastGC,
		"next_gc_bytes":     m.NextGC,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) trackedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=2")
	subs := s.deps.Tracker.ActiveSubs()
	for i := range subs {
		subs[i].Title = s.deps.LiveStore.EventTitle(subs[i].EventTicker)
	}
	events := s.deps.Tracker.ActiveEvents()
	scores, _ := s.deps.LiveStore.LatestScores(r.Context(), events)
	occTS, _ := s.deps.LiveStore.EventOccurrenceTS(r.Context(), events)
	tickTS, _ := s.deps.LiveStore.LatestTickTS(r.Context(), events)
	for i := range subs {
		subs[i].OccurrenceTS = occTS[subs[i].EventTicker]
		subs[i].LatestTickTS = tickTS[subs[i].EventTicker]
	}
	json.NewEncoder(w).Encode(map[string]any{
		"subs":           subs,
		"event_count":    len(events),
		"market_count":   len(subs),
		"scores":         scores,
		"latest_tick_ts": tickTS,
	})
}

func (s *Server) strategyListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	json.NewEncoder(w).Encode(map[string]any{
		"strategies": s.deps.Engine.AvailableStrategies(),
	})
}

func (s *Server) simulationHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")

	// Per-strategy summaries + cumulative P&L series from backtest_results.
	btRows, err := s.deps.DB.GetAllBacktestResults()
	if err != nil {
		s.deps.Log.Error("simulation: get backtest results", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	type strategySummary struct {
		Name       string             `json:"name"`
		MatchCount int                `json:"match_count"`
		RunTS      int64              `json:"run_ts"`
		Summary    backtest.Summary   `json:"summary"`
		CumPnL     []backtest.CumPnLPoint `json:"cum_pnl"`
	}

	summaries := make([]strategySummary, 0, len(btRows))
	for _, row := range btRows {
		var summary backtest.Summary
		if err := json.Unmarshal([]byte(row.SummaryJSON), &summary); err != nil {
			s.deps.Log.Error("simulation: unmarshal summary", "strategy", row.Strategy, "err", err)
			continue
		}
		var cumPnL []backtest.CumPnLPoint
		if row.CumPnLJSON != "" {
			if err := json.Unmarshal([]byte(row.CumPnLJSON), &cumPnL); err != nil {
				s.deps.Log.Error("simulation: unmarshal cum_pnl", "strategy", row.Strategy, "err", err)
			}
		}
		summaries = append(summaries, strategySummary{
			Name:       row.Strategy,
			MatchCount: row.MatchCount,
			RunTS:      row.RunTS,
			Summary:    summary,
			CumPnL:     cumPnL,
		})
	}

	// Per-strategy × per-day × per-band rows from simulation_insights.
	insightRows, err := s.deps.DB.GetAllSimulationInsights()
	if err != nil {
		s.deps.Log.Error("simulation: get insights", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	insightRunTS, _ := s.deps.DB.GetSimulationInsightRunTS()

	json.NewEncoder(w).Encode(map[string]any{
		"summaries":     summaries,
		"bands":         insightRows,
		"insight_run_ts": insightRunTS,
	})
}

func (s *Server) paperOrdersInsightsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")

	summaryRows, err := s.deps.DB.GetAllPaperOrderSummaries()
	if err != nil {
		s.deps.Log.Error("paper-orders-insights: get summaries", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	type summaryOut struct {
		Strategy      string                `json:"strategy"`
		TotalSignals  int                   `json:"total_signals"`
		Wins          int                   `json:"wins"`
		Losses        int                   `json:"losses"`
		WinRate       float64               `json:"win_rate"`
		TotalInvested float64               `json:"total_invested"`
		NetPnL        float64               `json:"net_pnl"`
		ROI           float64               `json:"roi"`
		AvgEdge       float64               `json:"avg_edge"`
		Sharpe        float64               `json:"sharpe"`
		ProfitFactor  float64               `json:"profit_factor"`
		MaxDrawdown   float64               `json:"max_drawdown"`
		CumPnL        []backtest.CumPnLPoint `json:"cum_pnl"`
	}

	summaries := make([]summaryOut, 0, len(summaryRows))
	for _, row := range summaryRows {
		var cumPnL []backtest.CumPnLPoint
		if row.CumPnLJSON != "" {
			if err := json.Unmarshal([]byte(row.CumPnLJSON), &cumPnL); err != nil {
				s.deps.Log.Error("paper-orders-insights: unmarshal cum_pnl", "strategy", row.Strategy, "err", err)
			}
		}
		summaries = append(summaries, summaryOut{
			Strategy:      row.Strategy,
			TotalSignals:  row.TotalSignals,
			Wins:          row.Wins,
			Losses:        row.Losses,
			WinRate:       row.WinRate,
			TotalInvested: row.TotalInvested,
			NetPnL:        row.NetPnL,
			ROI:           row.ROI,
			AvgEdge:       row.AvgEdge,
			Sharpe:        row.Sharpe,
			ProfitFactor:  row.ProfitFactor,
			MaxDrawdown:   row.MaxDrawdown,
			CumPnL:        cumPnL,
		})
	}

	insightRows, err := s.deps.DB.GetAllPaperOrderInsights()
	if err != nil {
		s.deps.Log.Error("paper-orders-insights: get insights", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	runTS, _ := s.deps.DB.GetPaperOrderInsightRunTS()

	json.NewEncoder(w).Encode(map[string]any{
		"summaries":     summaries,
		"bands":         insightRows,
		"insight_run_ts": runTS,
	})
}

func (s *Server) ticksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3")

	eventTicker := r.URL.Query().Get("event")
	if eventTicker == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "missing event param"})
		return
	}

	data, err := s.deps.LiveStore.GetEventTickPrices(r.Context(), eventTicker)
	if err != nil {
		s.deps.Log.Error("get event ticks", "event", eventTicker, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(data)
}

func (s *Server) ordersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var cursor *dashboarddata.PaperOrderCursor
	if tsStr := r.URL.Query().Get("cursor_ts"); tsStr != "" {
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err == nil {
			idStr := r.URL.Query().Get("cursor_id")
			id, _ := strconv.ParseInt(idStr, 10, 64)
			cursor = &dashboarddata.PaperOrderCursor{TS: ts, ID: id}
		}
	}

	orders, hasMore, next, err := s.deps.LiveStore.GetPaperOrdersPage(r.Context(), cursor, limit)
	if err != nil {
		s.deps.Log.Error("get paper orders page", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	summary, err := s.deps.LiveStore.GetPaperOrderSummary(r.Context())
	if err != nil {
		s.deps.Log.Error("get paper order summary", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	strategies, err := s.deps.LiveStore.GetPaperOrderStrategies(r.Context())
	if err != nil {
		s.deps.Log.Error("get paper order strategies", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(dashboarddata.PaperOrderResponse{
		Orders:     orders,
		Summary:    summary,
		Strategies: strategies,
		HasMore:    hasMore,
		NextCursor: next,
	})
}

func (s *Server) orderCountsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	counts, err := s.deps.LiveStore.GetOrderCountsByEvent(r.Context())
	if err != nil {
		s.deps.Log.Error("get order counts", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"counts": counts})
}

func (s *Server) pendingOrderCountsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	counts, err := s.deps.LiveStore.GetPendingOrderCountsByEvent(r.Context())
	if err != nil {
		s.deps.Log.Error("get pending order counts", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"counts": counts})
}

func (s *Server) passedMatchesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=10")

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}

	matches, err := s.deps.LiveStore.GetPassedMatches(r.Context(), limit)
	if err != nil {
		s.deps.Log.Error("get passed matches", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"matches": matches})
}

func (s *Server) realOrdersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	orders, err := s.deps.DB.GetRealOrders(r.Context())
	if err != nil {
		s.deps.Log.Error("get real orders", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"orders": orders})
}

func (s *Server) liquidityPoolHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")

	lp, err := liquiditypool.Get(r.Context(), s.deps.DB.GormDB())
	if err != nil {
		s.deps.Log.Error("get liquidity pool", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"balance_cents":         lp.BalanceCents,
		"initial_balance_cents": lp.InitialBalanceCents,
		"total_spent_cents":     lp.TotalSpentCents,
		"total_pnl_cents":       lp.TotalPNLCents,
		"updated_ts":            lp.UpdatedTS,
	})
}

// liquidityPoolResetHandler resets the pool to a new initial balance.
// Wipes total_spent, total_pnl. Use when changing the risk envelope.
// Body: {"balance_cents": 2000}
func (s *Server) liquidityPoolResetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]any{"error": "POST only"})
		return
	}
	var body struct {
		BalanceCents int64 `json:"balance_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid body"})
		return
	}
	if body.BalanceCents <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "balance_cents must be positive"})
		return
	}
	if err := liquiditypool.Reset(r.Context(), s.deps.DB.GormDB(), body.BalanceCents); err != nil {
		s.deps.Log.Error("reset liquidity pool", "err", err, "balance_cents", body.BalanceCents)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	s.deps.Log.Info("liquidity pool reset", "new_balance_cents", body.BalanceCents)
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "balance_cents": body.BalanceCents})
}

// liquidityPoolTopUpHandler adds capital to the pool without wiping history.
// Increases balance + initial_balance by addCents. Use when injecting more
// capital mid-run.
// Body: {"add_cents": 500}
func (s *Server) liquidityPoolTopUpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]any{"error": "POST only"})
		return
	}
	var body struct {
		AddCents int64 `json:"add_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid body"})
		return
	}
	if body.AddCents <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "add_cents must be positive"})
		return
	}
	if err := liquiditypool.TopUp(r.Context(), s.deps.DB.GormDB(), body.AddCents); err != nil {
		s.deps.Log.Error("topup liquidity pool", "err", err, "add_cents", body.AddCents)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	s.deps.Log.Info("liquidity pool topped up", "add_cents", body.AddCents)
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "add_cents": body.AddCents})
}

func (s *Server) strategyConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		entries, err := strategyconfig.GetAll(r.Context(), s.deps.DB.GormDB())
		if err != nil {
			s.deps.Log.Error("get strategy config", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"strategies": entries})

	case http.MethodPut:
		var body struct {
			Strategy           string `json:"strategy"`
			Enabled            *bool  `json:"enabled"`
			PerMarketMaxOrders *int   `json:"per_market_max_orders"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "invalid body"})
			return
		}
		if body.Strategy == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "strategy is required"})
			return
		}
		if body.Enabled != nil {
			if err := strategyconfig.SetEnabled(r.Context(), s.deps.DB.GormDB(), body.Strategy, *body.Enabled); err != nil {
				s.deps.Log.Error("set strategy enabled", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
		}
		if body.PerMarketMaxOrders != nil {
			if err := strategyconfig.SetLimit(r.Context(), s.deps.DB.GormDB(), body.Strategy, *body.PerMarketMaxOrders); err != nil {
				s.deps.Log.Error("set strategy per-market limit", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) triggerRangesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		strategy := r.URL.Query().Get("strategy")
		if strategy != "" {
			ranges, err := triggerranges.Get(r.Context(), s.deps.DB.GormDB(), strategy)
			if err != nil {
				s.deps.Log.Error("get trigger ranges", "strategy", strategy, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ranges": ranges})
		} else {
			entries, err := strategyconfig.GetAll(r.Context(), s.deps.DB.GormDB())
			if err != nil {
				s.deps.Log.Error("get strategy config for trigger ranges", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			result := make(map[string]any)
			for _, e := range entries {
				ranges, err := triggerranges.Get(r.Context(), s.deps.DB.GormDB(), e.Strategy)
				if err != nil {
					s.deps.Log.Error("get trigger ranges", "strategy", e.Strategy, "err", err)
					continue
				}
				result[e.Strategy] = ranges
			}
			json.NewEncoder(w).Encode(map[string]any{"ranges": result})
		}

	case http.MethodPut:
		var body struct {
			Strategy string               `json:"strategy"`
			Ranges   []store.TriggerRange `json:"ranges"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "invalid body"})
			return
		}
		if body.Strategy == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "strategy is required"})
			return
		}
		if err := triggerranges.Replace(r.Context(), s.deps.DB.GormDB(), body.Strategy, body.Ranges); err != nil {
			s.deps.Log.Error("replace trigger ranges", "strategy", body.Strategy, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) appConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		pairs := config.Cfg.GetAll()
		json.NewEncoder(w).Encode(map[string]any{"config": pairs})

	case http.MethodPut:
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "invalid body"})
			return
		}
		// Route through config.Update so writes refresh the live config.
		// Bypassing it (db.SetAppConfig directly) leaves the running process
		// holding stale startup values — real_trading_enabled flip never took.
		if err := config.Cfg.Update(body.Key, body.Value); err != nil {
			s.deps.Log.Error("set app config", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		s.deps.Log.Info("app config updated", "key", body.Key, "value", body.Value)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
