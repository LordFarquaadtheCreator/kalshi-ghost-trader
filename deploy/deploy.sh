#!/bin/bash
# Deploy ghost-trader to mint box.
# Builds backend + backtest locally, scp's binaries + dashboard source to remote,
# syncs remote git repo to match local HEAD, restarts services.
# Usage: ./deploy/deploy.sh [host] [--dashboard|--backend]
# Defaults: host=mint, deploy both dashboard + backend
set -euo pipefail

HOST="${1:-mint}"
shift || true
DEPLOY_DASHBOARD=0
DEPLOY_BACKEND=0
for arg in "$@"; do
  case "$arg" in
    --dashboard) DEPLOY_DASHBOARD=1 ;;
    --backend)   DEPLOY_BACKEND=1 ;;
    --*)         echo "==> ERROR: unknown flag '$arg' (expected --dashboard|--backend)" >&2; exit 1 ;;
    *)           ;; # ignore positional args (e.g. legacy "main")
  esac
done
# Default: both
if [ "$DEPLOY_DASHBOARD" -eq 0 ] && [ "$DEPLOY_BACKEND" -eq 0 ]; then
  DEPLOY_DASHBOARD=1
  DEPLOY_BACKEND=1
fi

REMOTE_DIR="/home/fahad/kalshi-ghost-trader"
OUT="deploy/out"

cd "$(dirname "$0")/.."

# Require clean local state matching remote HEAD — prevents deploying uncommitted code.
LOCAL_HEAD=$(git rev-parse HEAD)
LOCAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo "==> ERROR: uncommitted changes. Commit or stash first." >&2
  exit 1
fi
if ! git diff --quiet "origin/$LOCAL_BRANCH" "$LOCAL_BRANCH"; then
  echo "==> ERROR: local $LOCAL_BRANCH not pushed to origin. Push first." >&2
  exit 1
fi

echo "==> Deploy target: $HOST (dashboard=$DEPLOY_DASHBOARD backend=$DEPLOY_BACKEND)"

if [ "$DEPLOY_BACKEND" -eq 1 ]; then
  echo "==> Cleaning build output..."
  rm -rf "$OUT"
  mkdir -p "$OUT"

  echo "==> Building backend + backtest (linux/amd64)..."
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$OUT/ghost-trader" .
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$OUT/backtest" ./cmd/backtest

  echo "==> Copying service file..."
  cp deploy/ghost-trader.service "$OUT/"
fi

echo "==> Syncing remote git repo to $LOCAL_HEAD..."
ssh "$HOST" "cd $REMOTE_DIR && git fetch origin && git checkout $LOCAL_BRANCH && git reset --hard $LOCAL_HEAD && echo 'remote at $LOCAL_HEAD'"

if [ "$DEPLOY_BACKEND" -eq 1 ]; then
  echo "==> Stopping backend..."
  ssh "$HOST" 'sudo -n systemctl stop kalshi-ghost-trader'

  echo "==> Uploading binaries..."
  scp "$OUT/ghost-trader" "$HOST:$REMOTE_DIR/ghost-trader"
  scp "$OUT/backtest" "$HOST:$REMOTE_DIR/bin/backtest"
  ssh "$HOST" "chmod +x $REMOTE_DIR/ghost-trader $REMOTE_DIR/bin/backtest"

  echo "==> Syncing service file..."
  scp "$OUT/ghost-trader.service" "$HOST:/tmp/kalshi-ghost-trader.service"
  ssh "$HOST" 'diff -q /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service 2>/dev/null || sudo -n bash -c "cp /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service && systemctl daemon-reload" && echo "service file synced"'
fi

if [ "$DEPLOY_DASHBOARD" -eq 1 ]; then
  echo "==> Syncing dashboard source..."
  rsync -az --delete \
    --exclude 'node_modules' \
    --exclude 'build' \
    --exclude '.svelte-kit' \
    --exclude 'vite.config.js.timestamp-*' \
    dashboard/ "$HOST:$REMOTE_DIR/dashboard/"
fi

if [ "$DEPLOY_BACKEND" -eq 1 ]; then
  echo "==> Restarting backend..."
  ssh "$HOST" 'sudo -n systemctl restart kalshi-ghost-trader'
fi

if [ "$DEPLOY_DASHBOARD" -eq 1 ]; then
  echo "==> Restarting dashboard..."
  ssh "$HOST" 'sudo -n systemctl restart kalshi-dashboard'
fi

echo "==> Health check..."
ssh "$HOST" 'for i in $(seq 1 15); do code=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:6060/metrics 2>/dev/null); if [ "$code" = "200" ]; then break; fi; sleep 2; done; echo "backend: $code"; curl -s -o /dev/null -w "dashboard: %{http_code}\n" http://127.0.0.1:5173/'

echo "==> Done. Check logs with:"
echo "    ssh $HOST 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'"
