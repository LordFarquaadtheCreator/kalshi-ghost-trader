#!/bin/bash
# Deploy ghost-trader to mint box.
# Builds backend locally, scp's to remote, pulls code, restarts services.
# Dashboard runs via Vite dev server on mint — no build needed, just pull code.
# Usage: ./deploy/deploy.sh [host] [branch]
# Defaults: host=mint, branch=main
set -euo pipefail

HOST="${1:-mint}"
BRANCH="${2:-main}"
REMOTE_DIR="/home/fahad/kalshi-ghost-trader"
OUT="deploy/out"

cd "$(dirname "$0")/.."

echo "==> Cleaning build output..."
rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> Building backend (linux/amd64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$OUT/ghost-trader" .

echo "==> Copying service file..."
cp deploy/ghost-trader.service "$OUT/"

echo "==> Uploading backend..."
ssh "$HOST" 'sudo -n systemctl stop kalshi-ghost-trader'
scp "$OUT/ghost-trader" "$HOST:$REMOTE_DIR/ghost-trader"
ssh "$HOST" "chmod +x $REMOTE_DIR/ghost-trader"

echo "==> Syncing service file..."
scp "$OUT/ghost-trader.service" "$HOST:/tmp/kalshi-ghost-trader.service"
ssh "$HOST" 'diff -q /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service 2>/dev/null || sudo -n bash -c "cp /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service && systemctl daemon-reload" && echo "service file synced"'

echo "==> Pulling latest code..."
ssh "$HOST" "cd $REMOTE_DIR && git fetch origin && git checkout $BRANCH && git pull --ff-only origin $BRANCH"

echo "==> Restarting backend..."
ssh "$HOST" 'sudo -n systemctl restart kalshi-ghost-trader'

echo "==> Restarting dashboard..."
ssh "$HOST" 'sudo -n systemctl restart kalshi-dashboard'

echo "==> Health check..."
ssh "$HOST" 'for i in $(seq 1 15); do code=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:6060/metrics 2>/dev/null); if [ "$code" = "200" ]; then break; fi; sleep 2; done; echo "backend: $code"; curl -s -o /dev/null -w "dashboard: %{http_code}\n" http://127.0.0.1:5173/'

echo "==> Done. Check logs with:"
echo "    ssh $HOST 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'"
