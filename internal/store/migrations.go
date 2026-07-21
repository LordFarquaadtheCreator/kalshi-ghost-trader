package store

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// MigrationInfo describes a migration and its application status.
type MigrationInfo struct {
	Name    string
	Applied bool
	SQL     string
}

// migrationNames returns sorted list of embedded .sql migration filenames.
func migrationNames() ([]string, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// migrationSQL reads SQL content for a given migration filename.
func migrationSQL(name string) (string, error) {
	content, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return "", fmt.Errorf("read migration %s: %w", name, err)
	}
	return string(content), nil
}

// ListAllMigrations returns all embedded migrations with SQL content.
// Does not query the DB — purely reads from embedded files.
func ListAllMigrations() ([]MigrationInfo, error) {
	names, err := migrationNames()
	if err != nil {
		return nil, err
	}
	var result []MigrationInfo
	for _, name := range names {
		sql, err := migrationSQL(name)
		if err != nil {
			return nil, err
		}
		result = append(result, MigrationInfo{Name: name, SQL: sql})
	}
	return result, nil
}

// ListAllDBMigrations returns a set of migration names recorded in the schema_migrations table.
func (d *DB) ListAllDBMigrations() (map[string]bool, error) {
	var rows []SchemaMigration
	if err := d.db.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	result := make(map[string]bool, len(rows))
	for _, r := range rows {
		result[r.Name] = true
	}
	return result, nil
}

// ListUnappliedMigrations returns migrations on disk that haven't been applied to the DB yet.
// Migrations recorded in the DB but no longer on disk (e.g. removed .sql file) are intentionally ignored —
// the DB is source of truth for what was already applied, disk is source of truth for what still needs to run.
func (d *DB) ListUnappliedMigrations() ([]MigrationInfo, error) {
	all, err := ListAllMigrations()
	if err != nil {
		return nil, err
	}
	appliedSet, err := d.ListAllDBMigrations()
	if err != nil {
		return nil, err
	}
	var result []MigrationInfo
	for _, m := range all {
		if !appliedSet[m.Name] {
			result = append(result, m)
		}
	}
	return result, nil
}

// RunMigration applies a single migration by filename. No-op if already applied.
// Caller provides the SQL content.
func (d *DB) RunMigration(name, sql string) error {
	// has migration already been run?
	var count int64
	if err := d.db.Model(&SchemaMigration{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if count > 0 {
		return nil
	}
	if err := d.db.Exec(sql).Error; err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if err := d.db.Create(&SchemaMigration{Name: name, AppliedAt: nowMillis()}).Error; err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return nil
}

// RunAllMigrations applies all unapplied SQL migrations in order.
func (d *DB) RunAllMigrations() error {
	unapplied, err := d.ListUnappliedMigrations()
	if err != nil {
		return err
	}
	for _, m := range unapplied {
		if err := d.RunMigration(m.Name, m.SQL); err != nil {
			return err
		}
	}
	return nil
}
