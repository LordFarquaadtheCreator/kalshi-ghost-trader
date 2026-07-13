package main

import (
	"encoding/json"
	"net/http"
	"runtime"
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

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	resp := map[string]any{
		"goroutines":       runtime.NumGoroutine(),
		"num_cpu":          runtime.NumCPU(),
		"num_threads":      runtime.GOMAXPROCS(0),
		"heap_alloc_bytes": m.HeapAlloc,
		"heap_sys_bytes":   m.HeapSys,
		"heap_objects":     m.HeapObjects,
		"stack_inuse_bytes": m.StackInuse,
		"sys_bytes":        m.Sys,
		"mallocs":          m.Mallocs,
		"frees":            m.Frees,
		"gc_num":           m.NumGC,
		"gc_pause_ns":      m.PauseTotalNs,
		"gc_last_ns":       m.LastGC,
		"next_gc_bytes":    m.NextGC,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
