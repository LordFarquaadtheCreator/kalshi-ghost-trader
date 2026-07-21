#!/bin/bash
# Migration CLI wrapper for kalshi-ghost-trader.
#
# Usage:
#   ./scripts/migrate.sh -list                          list all migrations + status
#   ./scripts/migrate.sh -dry-run                       show pending migrations without applying
#   ./scripts/migrate.sh -apply 0001_foo.sql            apply specific migration
#   ./scripts/migrate.sh -only 0001_foo.sql             apply only this one, skip others
#   ./scripts/migrate.sh                                apply all pending
#   ./scripts/migrate.sh -db /path/to/db.db -list       custom db path

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

exec go run ./cmd/migrate "$@"
