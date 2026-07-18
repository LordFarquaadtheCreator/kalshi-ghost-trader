package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/farquaad/kalshi-ghost-trader/internal/tracker"
)

// corsMiddleware adds CORS headers for dashboard cross-origin requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

// metricsHandler returns Go runtime stats as JSON.
// GET /metrics — goroutines, heap, GC, threads.
// pprof endpoints available at /debug/pprof/*.
func metricsHandler(w http.ResponseWriter, r *http.Request) {
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

// trackedHandler returns live tracker state — markets currently subscribed via WS.
// GET /api/tracked — returns active market→event mappings.
func trackedHandler(tr *tracker.Tracker, e *backtest.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=2")
		subs := tr.ActiveSubs()
		for i := range subs {
			subs[i].Title = e.EventTitle(subs[i].EventTicker)
		}
		events := tr.ActiveEvents()
		scores, _ := e.LatestScores(r.Context(), events)
		occTS, _ := e.EventOccurrenceTS(r.Context(), events)
		for i := range subs {
			subs[i].OccurrenceTS = occTS[subs[i].EventTicker]
		}
		json.NewEncoder(w).Encode(map[string]any{
			"subs":         subs,
			"event_count":  len(events),
			"market_count": len(subs),
			"scores":       scores,
		})
	}
}

// corsHandler wraps a HandlerFunc with CORS headers for dashboard requests.
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

func strategyListHandler(e *backtest.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		json.NewEncoder(w).Encode(map[string]any{
			"strategies": e.AvailableStrategies(),
		})
	}
}

var btMu sync.Mutex

func backtestHandler(e *backtest.Engine, cache *backtest.Cache, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		strategiesParam := r.URL.Query().Get("strategies")
		minPrice := 0.0
		if v := r.URL.Query().Get("min_price"); v != "" {
			fmt.Sscanf(v, "%f", &minPrice)
		}

		var selected []string
		if strategiesParam == "" || strategiesParam == "all" {
			selected = e.AvailableStrategies()
		} else {
			selected = strings.Split(strategiesParam, ",")
		}

		btMu.Lock()
		defer btMu.Unlock()

		results := make([]*backtest.StrategyResult, 0, len(selected))
		for _, name := range selected {
			if cached := cache.Get(name, minPrice); cached != nil {
				results = append(results, cached)
				continue
			}
			res, err := e.RunStrategy(name, minPrice)
			if err != nil {
				log.Error("run strategy", "name", name, "err", err)
				continue
			}
			cache.Put(name, minPrice, res)
			results = append(results, res)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"results": results,
		})
	}
}

func ticksHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3")

		eventTicker := r.URL.Query().Get("event")
		if eventTicker == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "missing event param"})
			return
		}

		data, err := e.GetEventTickPrices(r.Context(), eventTicker)
		if err != nil {
			log.Error("get event ticks", "event", eventTicker, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(data)
	}
}

func ordersHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=5")

		data, err := e.GetAllPaperOrders(r.Context())
		if err != nil {
			log.Error("get paper orders", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(data)
	}
}

func orderCountsHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=5")

		counts, err := e.GetOrderCountsByEvent(r.Context())
		if err != nil {
			log.Error("get order counts", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"counts": counts})
	}
}

func pendingOrderCountsHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=5")

		counts, err := e.GetPendingOrderCountsByEvent(r.Context())
		if err != nil {
			log.Error("get pending order counts", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"counts": counts})
	}
}

func passedMatchesHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=10")

		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}

		matches, err := e.GetPassedMatches(r.Context(), limit)
		if err != nil {
			log.Error("get passed matches", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"matches": matches})
	}
}

func priceBandsHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			selected = e.AvailableStrategies()
		} else {
			selected = strings.Split(strategiesParam, ",")
		}

		btMu.Lock()
		defer btMu.Unlock()

		results := make(map[string]*backtest.PriceBandResult)
		for _, name := range selected {
			res, err := e.ComputePriceBands(name, metricName, minSamples)
			if err != nil {
				log.Error("compute price bands", "name", name, "err", err)
				continue
			}
			results[name] = res
		}

		json.NewEncoder(w).Encode(map[string]any{
			"metric":  metricName,
			"results": results,
		})
	}
}

func realOrdersHandler(db *store.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=5")

		orders, err := db.GetRealOrders(r.Context())
		if err != nil {
			log.Error("get real orders", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"orders": orders})
	}
}

func liquidityPoolHandler(db *store.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=5")

		lp, err := db.GetLiquidityPool(r.Context())
		if err != nil {
			log.Error("get liquidity pool", "err", err)
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
}

func strategyConfigHandler(db *store.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			entries, err := db.GetAllStrategyConfig(r.Context())
			if err != nil {
				log.Error("get strategy config", "err", err)
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
			if err := db.SetStrategyEnabled(r.Context(), body.Strategy, body.Enabled); err != nil {
				log.Error("set strategy config", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func triggerRangesHandler(db *store.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			strategy := r.URL.Query().Get("strategy")
			if strategy != "" {
				ranges, err := db.GetTriggerRanges(r.Context(), strategy)
				if err != nil {
					log.Error("get trigger ranges", "strategy", strategy, "err", err)
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"ranges": ranges})
			} else {
				entries, err := db.GetAllStrategyConfig(r.Context())
				if err != nil {
					log.Error("get strategy config for trigger ranges", "err", err)
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
					return
				}
				result := make(map[string]any)
				for _, e := range entries {
					ranges, err := db.GetTriggerRanges(r.Context(), e.Strategy)
					if err != nil {
						log.Error("get trigger ranges", "strategy", e.Strategy, "err", err)
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
			if err := db.ReplaceTriggerRanges(r.Context(), body.Strategy, body.Ranges); err != nil {
				log.Error("replace trigger ranges", "strategy", body.Strategy, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func appConfigHandler(db *store.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			pairs, err := db.GetAllAppConfig(r.Context())
			if err != nil {
				log.Error("get app config", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
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
			if err := db.SetAppConfig(r.Context(), body.Key, body.Value); err != nil {
				log.Error("set app config", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
