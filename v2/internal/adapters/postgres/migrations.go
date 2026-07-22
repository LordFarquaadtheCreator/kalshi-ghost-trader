package postgres

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// RunMigrations applies all pending SQL migrations in order.
// Tracks applied migrations in the schema_migrations_v2 table.
func RunMigrations(ctx context.Context, db *gorm.DB, log *slog.Logger) error {
	// Create tracking table if not exists.
	if err := db.WithContext(ctx).Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations_v2 (
			filename text PRIMARY KEY,
			applied_at bigint NOT NULL DEFAULT (extract(epoch from now()) * 1000)::bigint
		)
	`).Error; err != nil {
		return fmt.Errorf("create migration tracking table: %w", err)
	}

	// List embedded migration files.
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var upFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, e.Name())
		}
	}
	sort.Strings(upFiles)

	// Get applied set.
	type appliedRow struct {
		Filename string `gorm:"column:filename"`
	}
	var applied []appliedRow
	if err := db.WithContext(ctx).Table("schema_migrations_v2").Find(&applied).Error; err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, a := range applied {
		appliedSet[a.Filename] = true
	}

	for _, fname := range upFiles {
		if appliedSet[fname] {
			continue
		}

		data, err := migrationFS.ReadFile("migrations/" + fname)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", fname, err)
		}

		log.Info("migrations: applying", "file", fname)

		// Execute the migration. Each file may contain multiple statements.
		if err := db.WithContext(ctx).Exec(string(data)).Error; err != nil {
			return fmt.Errorf("apply migration %s: %w", fname, err)
		}

		// Record as applied.
		if err := db.WithContext(ctx).Exec(
			"INSERT INTO schema_migrations_v2 (filename) VALUES (?) ON CONFLICT DO NOTHING",
			fname,
		).Error; err != nil {
			return fmt.Errorf("record migration %s: %w", fname, err)
		}

		log.Info("migrations: applied", "file", fname)
	}

	return nil
}
