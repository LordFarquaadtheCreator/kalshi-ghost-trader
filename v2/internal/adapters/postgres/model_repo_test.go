package postgres

import (
	"context"
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/modelregistry"
)

func setupModelRepoDB(t *testing.T) *ModelRepo {
	t.Helper()
	db := testDB(t)
	sqlDB, _ := db.DB()

	_, err := sqlDB.Exec(`
		CREATE TABLE model_registry (
			id bigserial PRIMARY KEY,
			family text NOT NULL,
			version int NOT NULL,
			trained_at bigint NOT NULL,
			train_from_ts bigint NOT NULL,
			train_to_ts bigint NOT NULL,
			feature_hash text NOT NULL,
			artifact_path text NOT NULL,
			artifact_sha text NOT NULL,
			metrics jsonb NOT NULL,
			status text NOT NULL CHECK (status IN
				('candidate','shadow','paper','champion','retired')),
			trial_index int NOT NULL,
			UNIQUE (family, version)
		);
	`)
	if err != nil {
		t.Fatalf("create model_registry: %v", err)
	}

	return NewModelRepo(db)
}

func TestModelRepoRegisterAndGet(t *testing.T) {
	repo := setupModelRepoDB(t)
	ctx := context.Background()

	m := modelregistry.Model{
		Family:       modelregistry.FamilyFairValue,
		Version:      1,
		TrainedAt:    1700000000,
		TrainFromTS:  1699000000,
		TrainToTS:    1700000000,
		FeatureHash:  "abc123",
		ArtifactPath: "/models/fv1.txt",
		ArtifactSHA:  "sha123",
		Metrics:      map[string]any{"accuracy": 0.65},
	}

	id, err := repo.Register(ctx, m)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if id == 0 {
		t.Fatal("id = 0")
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Family != modelregistry.FamilyFairValue {
		t.Errorf("family = %s, want fairvalue", got.Family)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if got.Status != modelregistry.StatusCandidate {
		t.Errorf("status = %s, want candidate", got.Status)
	}
	if got.TrialIndex != 0 {
		t.Errorf("trial_index = %d, want 0 (first model)", got.TrialIndex)
	}
	if got.StrategyName() != "rl.fairvalue.v1" {
		t.Errorf("strategy_name = %s, want rl.fairvalue.v1", got.StrategyName())
	}
}

func TestModelRepoTrialIndexIncrements(t *testing.T) {
	repo := setupModelRepoDB(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		m := modelregistry.Model{
			Family:       modelregistry.FamilyFairValue,
			Version:      i,
			FeatureHash:  "abc",
			ArtifactPath: "/models/fv.txt",
			ArtifactSHA:  "sha",
			Metrics:      map[string]any{},
		}
		id, err := repo.Register(ctx, m)
		if err != nil {
			t.Fatalf("Register %d: %v", i, err)
		}
		got, _ := repo.Get(ctx, id)
		if got.TrialIndex != i-1 {
			t.Errorf("model %d: trial_index = %d, want %d", i, got.TrialIndex, i-1)
		}
	}
}

func TestModelRepoStatusTransitionsExhaustive(t *testing.T) {
	repo := setupModelRepoDB(t)
	ctx := context.Background()
	versionCounter := 0

	nextVersion := func() int {
		versionCounter++
		return versionCounter
	}

	// All legal transitions.
	legalTransitions := []struct {
		from, to modelregistry.Status
	}{
		{modelregistry.StatusCandidate, modelregistry.StatusShadow},
		{modelregistry.StatusShadow, modelregistry.StatusPaper},
		{modelregistry.StatusPaper, modelregistry.StatusChampion},
		{modelregistry.StatusPaper, modelregistry.StatusShadow},
		{modelregistry.StatusCandidate, modelregistry.StatusRetired},
		{modelregistry.StatusShadow, modelregistry.StatusRetired},
		{modelregistry.StatusPaper, modelregistry.StatusRetired},
		{modelregistry.StatusChampion, modelregistry.StatusRetired},
	}

	for _, tt := range legalTransitions {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			m := modelregistry.Model{
				Family:       modelregistry.FamilyFairValue,
				Version:      nextVersion(),
				FeatureHash:  "test",
				ArtifactPath: "/m",
				ArtifactSHA:  "s",
				Metrics:      map[string]any{},
			}
			id, err := repo.Register(ctx, m)
			if err != nil {
				t.Fatalf("Register: %v", err)
			}

			if err := walkToStatus(ctx, repo, id, tt.from); err != nil {
				t.Fatalf("walk to %s: %v", tt.from, err)
			}

			if err := repo.UpdateStatus(ctx, id, tt.to); err != nil {
				t.Fatalf("UpdateStatus %s→%s: %v", tt.from, tt.to, err)
			}

			got, _ := repo.Get(ctx, id)
			if got.Status != tt.to {
				t.Errorf("status = %s, want %s", got.Status, tt.to)
			}
		})
	}

	// All illegal transitions.
	illegalTransitions := []struct {
		from, to modelregistry.Status
	}{
		{modelregistry.StatusCandidate, modelregistry.StatusPaper},
		{modelregistry.StatusCandidate, modelregistry.StatusChampion},
		{modelregistry.StatusShadow, modelregistry.StatusChampion},
		{modelregistry.StatusChampion, modelregistry.StatusPaper},
		{modelregistry.StatusChampion, modelregistry.StatusShadow},
		{modelregistry.StatusChampion, modelregistry.StatusCandidate},
		{modelregistry.StatusRetired, modelregistry.StatusCandidate},
		{modelregistry.StatusRetired, modelregistry.StatusShadow},
		{modelregistry.StatusRetired, modelregistry.StatusPaper},
		{modelregistry.StatusRetired, modelregistry.StatusChampion},
	}

	for _, tt := range illegalTransitions {
		t.Run("illegal:"+string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			m := modelregistry.Model{
				Family:       modelregistry.FamilyFairValue,
				Version:      nextVersion(),
				FeatureHash:  "test",
				ArtifactPath: "/m",
				ArtifactSHA:  "s",
				Metrics:      map[string]any{},
			}
			id, err := repo.Register(ctx, m)
			if err != nil {
				t.Fatalf("Register: %v", err)
			}

			if err := walkToStatus(ctx, repo, id, tt.from); err != nil {
				t.Fatalf("walk to %s: %v", tt.from, err)
			}

			err = repo.UpdateStatus(ctx, id, tt.to)
			if err == nil {
				t.Fatalf("expected error for %s→%s, got nil", tt.from, tt.to)
			}
		})
	}
}

// walkToStatus advances a model from candidate to the target status
// via legal transitions only.
func walkToStatus(ctx context.Context, repo *ModelRepo, id int64, target modelregistry.Status) error {
	ladder := []modelregistry.Status{
		modelregistry.StatusCandidate,
		modelregistry.StatusShadow,
		modelregistry.StatusPaper,
		modelregistry.StatusChampion,
	}

	if target == modelregistry.StatusRetired {
		// Retired can be reached from any status — walk to candidate first
		// (already there), then retire.
		return repo.UpdateStatus(ctx, id, modelregistry.StatusRetired)
	}

	for i := 0; i < len(ladder)-1; i++ {
		if ladder[i] == target {
			return nil
		}
		if err := repo.UpdateStatus(ctx, id, ladder[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func TestModelRepoList(t *testing.T) {
	repo := setupModelRepoDB(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		_, _ = repo.Register(ctx, modelregistry.Model{
			Family:       modelregistry.FamilyFairValue,
			Version:      i,
			FeatureHash:  "h",
			ArtifactPath: "/m",
			ArtifactSHA:  "s",
			Metrics:      map[string]any{},
		})
	}

	models, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 3 {
		t.Errorf("list count = %d, want 3", len(models))
	}

	// Filter by family.
	models, _ = repo.List(ctx, "fairvalue")
	if len(models) != 3 {
		t.Errorf("filtered list count = %d, want 3", len(models))
	}

	models, _ = repo.List(ctx, "bandit")
	if len(models) != 0 {
		t.Errorf("non-existent family count = %d, want 0", len(models))
	}
}
