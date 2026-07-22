package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// openRawDB opens a low-level pgx connection used only by the test helper
// to create and drop per-test schemas outside of GORM's pool. The pgx
// dependency is already transitively present via the GORM postgres driver.
func openRawDB(ctx context.Context, dsn string) (*pgx.Conn, error) {
	return pgx.Connect(ctx, dsn)
}
