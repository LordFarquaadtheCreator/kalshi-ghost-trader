#!/bin/bash
# Cross-compile ghost-trader for remote ARM64
# Uses modernc.org/sqlite (pure Go, no CGO needed)
set -e

cd "$(dirname "$0")/.."

OUTPUT="ghost-trader"
TARGET_DIR="deploy/out"

mkdir -p "$TARGET_DIR"

echo "Building for linux/arm64..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o "$TARGET_DIR/$OUTPUT" ./cmd/ghost-trader

echo "Built: $TARGET_DIR/$OUTPUT"
file "$TARGET_DIR/$OUTPUT"
