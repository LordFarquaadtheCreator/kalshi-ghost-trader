package dashboardapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/liquiditypool"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/strategyconfig"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
	"github.com/farquaad/kalshi-ghost-trader/internal/triggerranges"
)

type Deps struct {
	Tracker *tracker.Tracker
	Engine  *backtest.Engine
	Cache   *backtest.Cache
	DB      *store.DB
	Log     *slog.Logger
}

type Server struct {
	deps Deps
	mu   sync.Mutex
}

func NewServer(deps Deps) *Server {
	return &Server{deps: deps}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/api/tracked", s.trackedHandler)
	mux.HandleFunc("/api/strategies", corsHandler(s.strategyListHandler))
	mux.HandleFunc("/api/backtest", corsHandler(s.backtestHandler))
	mux.HandleFunc("/api/price-bands", corsHandler(s.priceBandsHandler))
	mux.HandleFunc("/api/price-bands-snapshot", corsHandler(s.priceBandsSnapshotHandler))
	mux.HandleFunc("/api/ticks", corsHandler(s.ticksHandler))
	mux.HandleFunc("/api/orders", corsHandler(s.ordersHandler))
	mux.HandleFunc("/api/order-counts", corsHandler(s.orderCountsHandler))
	mux.HandleFunc("/api/pending-order-counts", corsHandler(s.pendingOrderCountsHandler))
	mux.HandleFunc("/api/passed-matches", corsHandler(s.passedMatchesHandler))
	mux.HandleFunc("/api/real-orders", corsHandler(s.realOrdersHandler))
	mux.HandleFunc("/api/liquidity-pool", corsHandler(s.liquidityPoolHandler))
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
		subs[i].Title = s.deps.Engine.EventTitle(subs[i].EventTicker)
	}
	events := s.deps.Tracker.ActiveEvents()
	scores, _ := s.deps.Engine.LatestScores(r.Context(), events)
	occTS, _ := s.deps.Engine.EventOccurrenceTS(r.Context(), events)
	tickTS, _ := s.deps.Engine.LatestTickTS(r.Context(), events)
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

func (s *Server) backtestHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	strategiesParam := r.URL.Query().Get("strategies")
	minPrice := 0.0
	if v := r.URL.Query().Get("min_price"); v != "" {
		fmt.Sscanf(v, "%f", &minPrice)
	}

	var selected []string
	if strategiesParam == "" || strategiesParam == "all" {
		selected = s.deps.Engine.AvailableStrategies()
	} else {
		selected = strings.Split(strategiesParam, ",")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]*backtest.StrategyResult, 0, len(selected))
	for _, name := range selected {
		if cached := s.deps.Cache.Get(name, minPrice); cached != nil {
			results = append(results, cached)
			continue
		}
		res, err := s.deps.Engine.RunStrategy(name, minPrice)
		if err != nil {
			s.deps.Log.Error("run strategy", "name", name, "err", err)
			continue
		}
		s.deps.Cache.Put(name, minPrice, res)
		results = append(results, res)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"results": results,
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

	data, err := s.deps.Engine.GetEventTickPrices(r.Context(), eventTicker)
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
	var cursor *backtest.PaperOrderCursor
	if tsStr := r.URL.Query().Get("cursor_ts"); tsStr != "" {
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err == nil {
			idStr := r.URL.Query().Get("cursor_id")
			id, _ := strconv.ParseInt(idStr, 10, 64)
			cursor = &backtest.PaperOrderCursor{TS: ts, ID: id}
		}
	}

	orders, hasMore, next, err := s.deps.Engine.GetPaperOrdersPage(r.Context(), cursor, limit)
	if err != nil {
		s.deps.Log.Error("get paper orders page", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	summary, err := s.deps.Engine.GetPaperOrderSummary(r.Context())
	if err != nil {
		s.deps.Log.Error("get paper order summary", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	strategies, err := s.deps.Engine.GetPaperOrderStrategies(r.Context())
	if err != nil {
		s.deps.Log.Error("get paper order strategies", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(backtest.PaperOrderResponse{
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

	counts, err := s.deps.Engine.GetOrderCountsByEvent(r.Context())
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

	counts, err := s.deps.Engine.GetPendingOrderCountsByEvent(r.Context())
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

	matches, err := s.deps.Engine.GetPassedMatches(r.Context(), limit)
	if err != nil {
		s.deps.Log.Error("get passed matches", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"matches": matches})
}

func (s *Server) priceBandsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	strategiesParam := r.URL.Query().Get("strategies")
	metricName := r.URL.Query().Get("metric")
	if metricName == "" {
		metricName = "winrate"
	}
	minSamples := 5
	if v := r.URL.Query().Get("min_samples"); v != "" {
		fmt.Sscanf(v, "%d", &minSamples)
	}

	var selected []string
	if strategiesParam == "" || strategiesParam == "all" {
		selected = s.deps.Engine.AvailableStrategies()
	} else {
		selected = strings.Split(strategiesParam, ",")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	results := make(map[string]*backtest.PriceBandResult)
	for _, name := range selected {
		res, err := s.deps.Engine.ComputePriceBands(name, metricName, minSamples)
		if err != nil {
			s.deps.Log.Error("compute price bands", "name", name, "err", err)
			continue
		}
		results[name] = res
	}

	json.NewEncoder(w).Encode(map[string]any{
		"metric":  metricName,
		"results": results,
	})
}

func (s *Server) priceBandsSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")

	rows, err := s.deps.DB.GetAllPriceBandResults()
	if err != nil {
		s.deps.Log.Error("get price band results", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	runTS, _ := s.deps.DB.GetPriceBandRunTS()

	json.NewEncoder(w).Encode(map[string]any{
		"run_ts":  runTS,
		"results": rows,
	})
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
			Strategy string `json:"strategy"`
			Enabled  bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "invalid body"})
			return
		}
		if err := strategyconfig.SetEnabled(r.Context(), s.deps.DB.GormDB(), body.Strategy, body.Enabled); err != nil {
			s.deps.Log.Error("set strategy config", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
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
