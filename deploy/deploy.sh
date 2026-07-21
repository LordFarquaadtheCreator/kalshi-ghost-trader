#!/bin/bash
# Deploy ghost-trader to mint box.
# Builds artifacts locally into deploy/out/, scp's to remote, restarts services.
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

echo "==> Building dashboard..."
cd dashboard && npm run build && cd ..
cp -r dashboard/build "$OUT/dashboard-build"

echo "==> Copying service file..."
cp deploy/ghost-trader.service "$OUT/"

echo "==> Uploading artifacts..."
scp "$OUT/ghost-trader" "$HOST:$REMOTE_DIR/ghost-trader"
ssh "$HOST" "chmod +x $REMOTE_DIR/ghost-trader"
scp "$OUT/ghost-trader.service" "$HOST:/tmp/kalshi-ghost-trader.service"
ssh "$HOST" 'sudo cmp -s /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service || { sudo cp /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service && sudo systemctl daemon-reload && echo "service file updated"; }'
ssh "$HOST" "rm -rf $REMOTE_DIR/dashboard/build"
scp -r "$OUT/dashboard-build" "$HOST:$REMOTE_DIR/dashboard/build"

echo "==> Pulling latest code..."
ssh "$HOST" "cd $REMOTE_DIR && git fetch origin && git checkout $BRANCH && git pull --ff-only origin $BRANCH"

echo "==> Restarting backend..."
ssh "$HOST" 'sudo -n systemctl restart kalshi-ghost-trader'
sleep 3

echo "==> Restarting dashboard..."
ssh "$HOST" 'sudo -n systemctl restart kalshi-dashboard'
sleep 2

echo "==> Health check..."
ssh "$HOST" 'curl -s -o /dev/null -w "backend: %{http_code}\n" http://127.0.0.1:6060/metrics && curl -s -o /dev/null -w "dashboard: %{http_code}\n" http://127.0.0.1:5173/'

echo "==> Done. Check logs with:"
echo "    ssh $HOST 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'"
