#!/bin/bash
# Deploy ghost-trader to mint box.
# Builds artifacts locally, scp's to remote, restarts services.
# Usage: ./scripts/update-remote.sh [host] [branch]
# Defaults: host=mint, branch=main
set -euo pipefail

HOST="${1:-mint}"
BRANCH="${2:-main}"
REMOTE_DIR="/home/fahad/kalshi-ghost-trader"
LOCAL_OUT="deploy/out"

cd "$(dirname "$0")/.."

echo "==> Building backend (linux/amd64)..."
mkdir -p "$LOCAL_OUT"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$LOCAL_OUT/ghost-trader" .

echo "==> Building dashboard..."
cd dashboard && npm run build && cd ..

echo "==> Syncing service file..."
cat > /tmp/kalshi-ghost-trader.service << 'EOF'
[Unit]
Description=Kalshi Ghost Trader Backend
After=network.target

[Service]
Type=simple
User=fahad
WorkingDirectory=/home/fahad/kalshi-ghost-trader
Environment=APP_ENV=prod
ExecStart=/home/fahad/kalshi-ghost-trader/ghost-trader
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
scp /tmp/kalshi-ghost-trader.service "$HOST:/tmp/kalshi-ghost-trader.service"
ssh "$HOST" 'sudo cmp -s /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service || { sudo cp /tmp/kalshi-ghost-trader.service /etc/systemd/system/kalshi-ghost-trader.service && sudo systemctl daemon-reload && echo "service file updated"; }'

echo "==> Uploading backend..."
scp "$LOCAL_OUT/ghost-trader" "$HOST:$REMOTE_DIR/ghost-trader"
ssh "$HOST" "chmod +x $REMOTE_DIR/ghost-trader"

echo "==> Uploading dashboard..."
ssh "$HOST" "rm -rf $REMOTE_DIR/dashboard/build"
scp -r dashboard/build "$HOST:$REMOTE_DIR/dashboard/build"

echo "==> Pulling latest code (for migrations, configs, ML models)..."
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
