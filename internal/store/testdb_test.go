package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"testing"
)

// testDB opens a Postgres-backed *DB for testing, isolated per test via a
// unique schema. Skips when TEST_DB_DSN is unset.
//
// Isolation strategy: create a uniquely named schema, set the connection's
// search_path to it, run migrations into that schema, and DROP SCHEMA ...
// CASCADE in tb.Cleanup. This avoids cross-test interference without a
// per-test database (which the kalshi role cannot create).
//
// The migration runner targets the current search_path because no migration
// SQL references an explicit schema — verified by grep at task time.
func testDB(tb testing.TB) *DB {
	tb.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		tb.Skip("TEST_DB_DSN not set; skipping Postgres-backed store tests")
	}

	// Unique schema name per test.
	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_" + hex.EncodeToString(b[:])

	// Create the schema before opening GORM so the search_path option applies.
	// Use a fresh connection for the DDL, then close it.
	{
		db, err := openRawDB(context.Background(), dsn)
		if err != nil {
			tb.Fatalf("open raw db for schema create: %v", err)
		}
		if _, err := db.Exec(context.Background(),
			fmt.Sprintf("CREATE SCHEMA %s", pqIdent(schema))); err != nil {
			db.Close(context.Background())
			tb.Fatalf("create schema %s: %v", schema, err)
		}
		db.Close(context.Background())
	}

	// Append search_path to the DSN so every pooled connection uses it.
	// Include public so database-level extensions (pg_trgm) are visible.
	schemaDSN := dsn + " search_path=" + schema + ",public"

	db, err := New(context.Background(), schemaDSN, slog.Default())
	if err != nil {
		tb.Fatalf("New: %v", err)
	}

	if err := db.Migrate(); err != nil {
		db.Close()
		// Drop the schema on migration failure too.
		dropTestSchema(tb, dsn, schema)
		tb.Fatalf("Migrate: %v", err)
	}

	tb.Cleanup(func() {
		db.Close()
		if keepSchema := os.Getenv("TEST_KEEP_SCHEMA"); keepSchema != "" {
			tb.Logf("keeping schema %s (TEST_KEEP_SCHEMA set)", schema)
		} else {
			dropTestSchema(tb, dsn, schema)
		}
	})

	return db
}

// dropTestSchema drops the per-test schema, ignoring errors so one test's
// cleanup failure doesn't mask another's failure.
func dropTestSchema(tb testing.TB, dsn, schema string) {
	tb.Helper()
	db, err := openRawDB(context.Background(), dsn)
	if err != nil {
		tb.Logf("open raw db for schema drop: %v", err)
		return
	}
	defer db.Close(context.Background())
	if _, err := db.Exec(context.Background(),
		fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pqIdent(schema))); err != nil {
		tb.Logf("drop schema %s: %v", schema, err)
	}
}

// pqIdent quotes an identifier for safe interpolation into Postgres DDL.
// Schema names from testDB are hex-encoded so this is belt-and-braces.
func pqIdent(name string) string {
	return "\"" + name + "\""
}
