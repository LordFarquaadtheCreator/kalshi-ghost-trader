package postgres

import (
	"context"
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

func TestFeatureRepoLogAndGet(t *testing.T) {
	db := testDB(t)
	sqlDB, _ := db.DB()

	_, err := sqlDB.Exec(`
		CREATE TABLE intent_features (
			order_id bigint PRIMARY KEY,
			feature_hash text NOT NULL,
			features jsonb NOT NULL,
			model_id bigint,
			propensity double precision
		);
		CREATE TABLE orders_v2 (
			id bigserial PRIMARY KEY,
			client_order_id uuid NOT NULL DEFAULT gen_random_uuid(),
			ts_intent bigint NOT NULL,
			event_ticker text NOT NULL,
			market_ticker text NOT NULL,
			strategy text NOT NULL,
			action text NOT NULL,
			contracts int NOT NULL,
			price_cents int NOT NULL,
			conv_prob_bps int NOT NULL,
			status text NOT NULL DEFAULT 'intent',
			is_paper boolean NOT NULL DEFAULT true
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	repo := NewFeatureRepo(db)
	ctx := context.Background()

	var orderID int64
	db.Raw("INSERT INTO orders_v2 (ts_intent, event_ticker, market_ticker, strategy, action, contracts, price_cents, conv_prob_bps) VALUES (0, 'E', 'M', 'test', 'buy', 1, 50, 6000) RETURNING id").Scan(&orderID)

	modelID := int64(42)
	prop := 0.75
	err = repo.LogFeatures(ctx, orderID, ports.FeatureLog{
		FeatureHash: "abc123",
		Features:    map[string]float64{"edge": 5.0, "spread": 2.0},
		ModelID:     &modelID,
		Propensity:  &prop,
	})
	if err != nil {
		t.Fatalf("LogFeatures: %v", err)
	}

	got, err := repo.GetFeatures(ctx, orderID)
	if err != nil {
		t.Fatalf("GetFeatures: %v", err)
	}

	if got.FeatureHash != "abc123" {
		t.Errorf("feature_hash = %s, want abc123", got.FeatureHash)
	}
	if got.Features["edge"] != 5.0 {
		t.Errorf("features[edge] = %f, want 5.0", got.Features["edge"])
	}
	if got.ModelID == nil || *got.ModelID != 42 {
		t.Errorf("model_id = %v, want 42", got.ModelID)
	}
	if got.Propensity == nil || *got.Propensity != 0.75 {
		t.Errorf("propensity = %v, want 0.75", got.Propensity)
	}
}

func TestFeatureRepoGatedIntent(t *testing.T) {
	db := testDB(t)
	sqlDB, _ := db.DB()

	_, err := sqlDB.Exec(`
		CREATE TABLE intent_features (
			order_id bigint PRIMARY KEY,
			feature_hash text NOT NULL,
			features jsonb NOT NULL,
			model_id bigint,
			propensity double precision
		);
		CREATE TABLE orders_v2 (
			id bigserial PRIMARY KEY,
			client_order_id uuid NOT NULL DEFAULT gen_random_uuid(),
			ts_intent bigint NOT NULL,
			event_ticker text NOT NULL,
			market_ticker text NOT NULL,
			strategy text NOT NULL,
			action text NOT NULL,
			contracts int NOT NULL,
			price_cents int NOT NULL,
			conv_prob_bps int NOT NULL,
			status text NOT NULL DEFAULT 'intent',
			gate_reason text,
			is_paper boolean NOT NULL DEFAULT true
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	repo := NewFeatureRepo(db)
	ctx := context.Background()

	var orderID int64
	db.Raw("INSERT INTO orders_v2 (ts_intent, event_ticker, market_ticker, strategy, action, contracts, price_cents, conv_prob_bps, status, gate_reason) VALUES (0, 'E', 'M', 'test', 'buy', 1, 50, 6000, 'gated', 'price_band') RETURNING id").Scan(&orderID)

	// Gated intents must still produce a feature row (A.2.1).
	err = repo.LogFeatures(ctx, orderID, ports.FeatureLog{
		FeatureHash: "def456",
		Features:    map[string]float64{"edge": -1.0},
	})
	if err != nil {
		t.Fatalf("LogFeatures for gated intent: %v", err)
	}

	got, err := repo.GetFeatures(ctx, orderID)
	if err != nil {
		t.Fatalf("GetFeatures: %v", err)
	}
	if got.FeatureHash != "def456" {
		t.Errorf("feature_hash = %s, want def456", got.FeatureHash)
	}

	var status string
	db.Raw("SELECT status FROM orders_v2 WHERE id = ?", orderID).Scan(&status)
	if status != "gated" {
		t.Errorf("order status = %s, want gated", status)
	}
}
