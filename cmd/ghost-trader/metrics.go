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
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
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

func backtestHandler(e *backtest.Engine, log *slog.Logger) http.HandlerFunc {
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
			res, err := e.RunStrategy(name, minPrice)
			if err != nil {
				log.Error("run strategy", "name", name, "err", err)
				continue
			}
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
