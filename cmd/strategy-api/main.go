// Command strategy-api is a localhost-only HTTP server that runs backtest
// strategies against the SQLite DB and returns results as JSON. Designed
// for the dashboard strategy outcomes page. No auth, no security.
//
// Usage:
//
//	go run ./cmd/strategy-api -db kalshi_tennis.db -port 6061
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/farquaad/kalshi-ghost-trader/internal/backtest"
)

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

func main() {
	dbPath := flag.String("db", "kalshi_tennis.db", "path to SQLite DB")
	port := flag.Int("port", 6061, "HTTP server port (localhost only)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	engine, err := backtest.NewEngine(*dbPath, log)
	if err != nil {
		log.Error("engine init", "err", err)
		os.Exit(1)
	}
	defer engine.Close()

	log.Info("strategy-api ready", "port", *port, "strategies", strings.Join(engine.AvailableStrategies(), ", "))

	mux := http.NewServeMux()

	mux.HandleFunc("/api/strategies", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"strategies": engine.AvailableStrategies(),
		})
	}))

	var mu sync.Mutex

	mux.HandleFunc("/api/backtest", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		strategiesParam := r.URL.Query().Get("strategies")
		minPrice := 0.0
		if v := r.URL.Query().Get("min_price"); v != "" {
			fmt.Sscanf(v, "%f", &minPrice)
		}

		var selected []string
		if strategiesParam == "" || strategiesParam == "all" {
			selected = engine.AvailableStrategies()
		} else {
			selected = strings.Split(strategiesParam, ",")
		}

		// Serialize backtest runs — they share the same DB reader
		mu.Lock()
		defer mu.Unlock()

		results := make([]*backtest.StrategyResult, 0, len(selected))
		for _, name := range selected {
			res, err := engine.RunStrategy(name, minPrice)
			if err != nil {
				log.Error("run strategy", "name", name, "err", err)
				continue
			}
			results = append(results, res)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"results": results,
		})
	}))

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
