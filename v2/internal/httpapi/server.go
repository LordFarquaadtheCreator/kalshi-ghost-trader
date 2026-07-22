// Package httpapi implements the v2 dashboard API.
// Stdlib mux — no new dependencies. Bearer token auth on all routes except /healthz.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/postgres"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/app/tracker"
	"gorm.io/gorm"
)

// Server is the HTTP API server.
type Server struct {
	db        *gorm.DB
	log       *slog.Logger
	token     string
	mux       *http.ServeMux
	sse       *sseHub
	modelRepo *postgres.ModelRepo
	tracker   *tracker.Tracker
}

// SetTracker wires the tracker for /api/tracked.
func (s *Server) SetTracker(t *tracker.Tracker) { s.tracker = t }

// NewServer creates an HTTP API server. Token comes from env API_TOKEN.
func NewServer(db *gorm.DB, log *slog.Logger) *Server {
	token := os.Getenv("API_TOKEN")
	s := &Server{
		db:    db,
		log:   log,
		token: token,
		sse:   newSSEHub(),
	}
	if db != nil {
		s.modelRepo = postgres.NewModelRepo(db)
	}
	s.mux = s.buildMux()
	return s
}

// buildMux wires all routes.
func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	// API v1 routes — all require bearer token.
	mux.HandleFunc("GET /api/v1/overview", s.auth(s.handleOverview))
	mux.HandleFunc("GET /api/v1/strategies", s.auth(s.handleStrategies))
	mux.HandleFunc("GET /api/v1/strategies/{name}", s.auth(s.handleStrategyDetail))
	mux.HandleFunc("GET /api/v1/matches", s.auth(s.handleMatches))
	mux.HandleFunc("GET /api/v1/matches/{event}", s.auth(s.handleMatchDetail))
	mux.HandleFunc("GET /api/v1/orders", s.auth(s.handleOrders))
	mux.HandleFunc("GET /api/v1/ledger", s.auth(s.handleLedger))
	mux.HandleFunc("GET /api/v1/config", s.auth(s.handleGetConfig))
	mux.HandleFunc("PUT /api/v1/config", s.auth(s.handlePutConfig))
	mux.HandleFunc("GET /api/v1/models", s.auth(s.handleModels))
	mux.HandleFunc("POST /api/v1/models", s.auth(s.handleModels))
	mux.HandleFunc("POST /api/v1/models/{id}/status", s.auth(s.handleModelStatus))
	mux.HandleFunc("GET /api/v1/stream", s.auth(s.handleStream))

	// v1-compatible routes for dashboard pages not yet migrated to v1 API.
	mux.HandleFunc("GET /api/tracked", s.cors(s.handleTracked))
	mux.HandleFunc("GET /api/order-counts", s.cors(s.handleOrderCounts))
	mux.HandleFunc("GET /api/pending-order-counts", s.cors(s.handlePendingOrderCounts))

	return mux
}

// Serve starts the HTTP server. Blocks until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	s.log.Info("httpapi: serving", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// auth wraps a handler with bearer token authentication.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeProblem(w, http.StatusUnauthorized, "missing_bearer", "Bearer token required")
			return
		}
		if strings.TrimPrefix(auth, "Bearer ") != s.token {
			writeProblem(w, http.StatusUnauthorized, "invalid_token", "Invalid bearer token")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
}

// writeJSON sends a JSON envelope response.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// writeProblem sends an RFC 7807 problem response.
func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}

// parseCursor extracts cursor and limit from query params.
func parseCursor(r *http.Request) (cursor string, limit int) {
	cursor = r.URL.Query().Get("cursor")
	limit = 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	return
}

// parseInt is a helper for parsing integer query params.
func parseInt(s string) (int, error) {
	return strconv.Atoi(s)
}

var _ = fmt.Sprintf
