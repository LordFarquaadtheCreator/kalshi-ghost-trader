package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/modelregistry"
)

// handleModels handles GET (list) and POST (register) for models.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listModels(w, r)
	case http.MethodPost:
		s.registerModel(w, r)
	default:
		writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and POST supported")
	}
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	family := r.URL.Query().Get("family")
	models, err := s.modelRepo.List(r.Context(), family)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out := make([]modelregistry.ModelJSON, len(models))
	for i, m := range models {
		out[i] = m.ToJSON()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) registerModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Family       string         `json:"family"`
		Version      int            `json:"version"`
		TrainedAt    int64          `json:"trained_at"`
		TrainFromTS  int64          `json:"train_from_ts"`
		TrainToTS    int64          `json:"train_to_ts"`
		FeatureHash  string         `json:"feature_hash"`
		ArtifactPath string         `json:"artifact_path"`
		ArtifactSHA  string         `json:"artifact_sha"`
		Metrics      map[string]any `json:"metrics"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if body.Family == "" || body.FeatureHash == "" || body.ArtifactPath == "" {
		writeProblem(w, http.StatusBadRequest, "missing_fields", "family, feature_hash, artifact_path required")
		return
	}

	m := modelregistry.Model{
		Family:       modelregistry.Family(body.Family),
		Version:      body.Version,
		TrainedAt:    body.TrainedAt,
		TrainFromTS:  body.TrainFromTS,
		TrainToTS:    body.TrainToTS,
		FeatureHash:  body.FeatureHash,
		ArtifactPath: body.ArtifactPath,
		ArtifactSHA:  body.ArtifactSHA,
		Metrics:      body.Metrics,
	}

	id, err := s.modelRepo.Register(r.Context(), m)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "register_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "candidate"})
}

// handleModelStatus handles POST /api/v1/models/{id}/status — promote/demote.
func (s *Server) handleModelStatus(w http.ResponseWriter, r *http.Request) {
	if !s.checkDB(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST supported")
		return
	}

	idStr := r.PathValue("id")
	if idStr == "" {
		writeProblem(w, http.StatusBadRequest, "missing_id", "Model ID required")
		return
	}

	id, err := parseInt64(idStr)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_id", "Model ID must be numeric")
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	to := modelregistry.Status(body.Status)
	switch to {
	case modelregistry.StatusCandidate, modelregistry.StatusShadow,
		modelregistry.StatusPaper, modelregistry.StatusChampion, modelregistry.StatusRetired:
		// valid
	default:
		writeProblem(w, http.StatusBadRequest, "invalid_status", "Unknown status: "+body.Status)
		return
	}

	// Champion promotion is human-gated — require explicit confirmation.
	if to == modelregistry.StatusChampion {
		confirm := r.URL.Query().Get("confirm")
		if confirm != "true" {
			writeProblem(w, http.StatusForbidden, "human_gate", "Champion promotion requires ?confirm=true")
			return
		}
	}

	if err := s.modelRepo.UpdateStatus(r.Context(), id, to); err != nil {
		// Check if it's a transition error.
		if _, ok := err.(*modelregistry.TransitionError); ok {
			writeProblem(w, http.StatusConflict, "illegal_transition", err.Error())
			return
		}
		writeProblem(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": body.Status})
}

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := parseInt64Impl(s, &n)
	return n, err
}
