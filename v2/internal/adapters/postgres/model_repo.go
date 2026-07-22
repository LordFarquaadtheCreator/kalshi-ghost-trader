package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/modelregistry"
	"gorm.io/gorm"
)

// ModelRepo persists model registry entries.
type ModelRepo struct {
	db *gorm.DB
}

// NewModelRepo creates a model repository.
func NewModelRepo(db *gorm.DB) *ModelRepo {
	return &ModelRepo{db: db}
}

type modelRow struct {
	ID            int64           `gorm:"primaryKey;column:id"`
	Family        string          `gorm:"column:family"`
	Version       int             `gorm:"column:version"`
	TrainedAt     int64           `gorm:"column:trained_at"`
	TrainFromTS   int64           `gorm:"column:train_from_ts"`
	TrainToTS     int64           `gorm:"column:train_to_ts"`
	FeatureHash   string          `gorm:"column:feature_hash"`
	ArtifactPath  string          `gorm:"column:artifact_path"`
	ArtifactSHA   string          `gorm:"column:artifact_sha"`
	Metrics       json.RawMessage `gorm:"column:metrics"`
	Status        string          `gorm:"column:status"`
	TrialIndex    int             `gorm:"column:trial_index"`
}

func (modelRow) TableName() string { return "model_registry" }

// Register inserts a new model as a candidate.
func (r *ModelRepo) Register(ctx context.Context, m modelregistry.Model) (int64, error) {
	metricsJSON, err := json.Marshal(m.Metrics)
	if err != nil {
		return 0, fmt.Errorf("marshal metrics: %w", err)
	}

	// Compute trial_index — count existing models in this family.
	var count int64
	if err := r.db.WithContext(ctx).Model(&modelRow{}).
		Where("family = ?", string(m.Family)).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count models: %w", err)
	}
	m.TrialIndex = int(count)

	row := modelRow{
		Family:       string(m.Family),
		Version:      m.Version,
		TrainedAt:    m.TrainedAt,
		TrainFromTS:  m.TrainFromTS,
		TrainToTS:    m.TrainToTS,
		FeatureHash:  m.FeatureHash,
		ArtifactPath: m.ArtifactPath,
		ArtifactSHA:  m.ArtifactSHA,
		Metrics:      metricsJSON,
		Status:       string(modelregistry.StatusCandidate),
		TrialIndex:   m.TrialIndex,
	}

	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, fmt.Errorf("create model: %w", err)
	}
	return row.ID, nil
}

// Get retrieves a model by ID.
func (r *ModelRepo) Get(ctx context.Context, id int64) (*modelregistry.Model, error) {
	var row modelRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, fmt.Errorf("get model: %w", err)
	}
	return rowToModel(&row), nil
}

// List returns all models, optionally filtered by family.
func (r *ModelRepo) List(ctx context.Context, family string) ([]modelregistry.Model, error) {
	query := r.db.WithContext(ctx).Model(&modelRow{}).Order("id DESC")
	if family != "" {
		query = query.Where("family = ?", family)
	}

	var rows []modelRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}

	models := make([]modelregistry.Model, len(rows))
	for i, row := range rows {
		models[i] = *rowToModel(&row)
	}
	return models, nil
}

// UpdateStatus transitions a model's status. Validates the transition is legal.
func (r *ModelRepo) UpdateStatus(ctx context.Context, id int64, to modelregistry.Status) error {
	var row modelRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return fmt.Errorf("get model for transition: %w", err)
	}

	from := modelregistry.Status(row.Status)
	if err := modelregistry.ValidateTransition(from, to); err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Model(&modelRow{}).
		Where("id = ?", id).
		Update("status", string(to)).Error; err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

func rowToModel(row *modelRow) *modelregistry.Model {
	var metrics map[string]any
	_ = json.Unmarshal(row.Metrics, &metrics)

	return &modelregistry.Model{
		ID:           row.ID,
		Family:       modelregistry.Family(row.Family),
		Version:      row.Version,
		TrainedAt:    row.TrainedAt,
		TrainFromTS:  row.TrainFromTS,
		TrainToTS:    row.TrainToTS,
		FeatureHash:  row.FeatureHash,
		ArtifactPath: row.ArtifactPath,
		ArtifactSHA:  row.ArtifactSHA,
		Metrics:      metrics,
		Status:       modelregistry.Status(row.Status),
		TrialIndex:   row.TrialIndex,
	}
}
