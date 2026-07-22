// Package modelregistry implements the model registry and promotion ladder.
// Models advance one rung at a time: candidate → shadow → paper → champion.
// Demotion is automatic and one-way until retrained.
package modelregistry

import "fmt"

// Status is the promotion ladder state of a model.
type Status string

const (
	StatusCandidate Status = "candidate"
	StatusShadow    Status = "shadow"
	StatusPaper     Status = "paper"
	StatusChampion  Status = "champion"
	StatusRetired   Status = "retired"
)

// Family is the model family.
type Family string

const (
	FamilyFairValue  Family = "fairvalue"
	FamilyBandit     Family = "bandit"
	FamilySequential Family = "sequential"
)

// Model is a registered model artifact.
type Model struct {
	ID            int64          `json:"id"`
	Family        Family         `json:"family"`
	Version       int            `json:"version"`
	TrainedAt     int64          `json:"trained_at"`
	TrainFromTS   int64          `json:"train_from_ts"`
	TrainToTS     int64          `json:"train_to_ts"`
	FeatureHash   string         `json:"feature_hash"`
	ArtifactPath  string         `json:"artifact_path"`
	ArtifactSHA   string         `json:"artifact_sha"`
	Metrics       map[string]any `json:"metrics"`
	Status        Status         `json:"status"`
	TrialIndex    int            `json:"trial_index"`
}

// ModelJSON is the JSON representation of a Model, including derived fields.
type ModelJSON struct {
	ID            int64          `json:"id"`
	Family        string         `json:"family"`
	Version       int            `json:"version"`
	TrainedAt     int64          `json:"trained_at"`
	TrainFromTS   int64          `json:"train_from_ts"`
	TrainToTS     int64          `json:"train_to_ts"`
	FeatureHash   string         `json:"feature_hash"`
	ArtifactPath  string         `json:"artifact_path"`
	ArtifactSHA   string         `json:"artifact_sha"`
	Metrics       map[string]any `json:"metrics"`
	Status        string         `json:"status"`
	TrialIndex    int            `json:"trial_index"`
	StrategyName  string         `json:"strategy_name"`
}

// ToJSON converts a Model to its JSON representation.
func (m Model) ToJSON() ModelJSON {
	return ModelJSON{
		ID:            m.ID,
		Family:        string(m.Family),
		Version:       m.Version,
		TrainedAt:     m.TrainedAt,
		TrainFromTS:   m.TrainFromTS,
		TrainToTS:     m.TrainToTS,
		FeatureHash:   m.FeatureHash,
		ArtifactPath:  m.ArtifactPath,
		ArtifactSHA:   m.ArtifactSHA,
		Metrics:       m.Metrics,
		Status:        string(m.Status),
		TrialIndex:    m.TrialIndex,
		StrategyName:  m.StrategyName(),
	}
}

// StrategyName returns the strategy name for this model.
// Format: rl.<family>.v<version>
func (m Model) StrategyName() string {
	return fmt.Sprintf("rl.%s.v%d", m.Family, m.Version)
}

// legalTransitions defines the promotion ladder.
// A model advances one rung at a time, never skipping.
// Demotion to retired is always allowed from any non-retired status.
var legalTransitions = map[Status]map[Status]bool{
	StatusCandidate: {
		StatusShadow:  true, // promote
		StatusRetired: true, // demote
	},
	StatusShadow: {
		StatusPaper:   true, // promote
		StatusRetired: true, // demote
	},
	StatusPaper: {
		StatusChampion: true, // promote (human-gated)
		StatusShadow:   true, // demote (drawdown/drift)
		StatusRetired:  true, // demote
	},
	StatusChampion: {
		StatusRetired: true, // demote only — no backward promotion
	},
	StatusRetired: {}, // terminal — no transitions
}

// CanTransition returns true if from→to is a legal transition.
func CanTransition(from, to Status) bool {
	allowed, ok := legalTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// ValidateTransition checks whether a transition is legal and returns an
// error explaining why if not.
func ValidateTransition(from, to Status) error {
	if from == to {
		return &TransitionError{From: from, To: to, Reason: "no-op transition"}
	}
	if !CanTransition(from, to) {
		return &TransitionError{From: from, To: to, Reason: "must advance one rung at a time"}
	}
	return nil
}

// TransitionError is returned when a status transition is illegal.
type TransitionError struct {
	From   Status
	To     Status
	Reason string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("illegal transition: %s → %s (%s)", e.From, e.To, e.Reason)
}
