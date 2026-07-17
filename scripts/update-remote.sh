#!/bin/bash
# Update ghost-trader on remote mint box.
# Pulls latest, rebuilds, restarts services.
# Usage: ./scripts/update-remote.sh [host]
# Default host: mint (from ~/.ssh/config)
set -e

HOST="${1:-mint}"
REMOTE_DIR="/home/fahad/kalshi-ghost-trader"

echo "==> Stashing mint-deploy-tweaks..."
ssh "$HOST" "cd $REMOTE_DIR && git stash push -u -m 'mint-deploy-tweaks'"

echo "==> Pulling latest..."
ssh "$HOST" "cd $REMOTE_DIR && git pull --ff-only origin main"

echo "==> Popping stash..."
ssh "$HOST" "cd $REMOTE_DIR && git stash pop"

echo "==> Building binary..."
ssh "$HOST" "cd $REMOTE_DIR && go build -o ghost-trader ./cmd/ghost-trader"

echo "==> Restarting ghost-trader..."
ssh "$HOST" "sudo -n systemctl restart kalshi-ghost-trader"

sleep 2

echo "==> Restarting dashboard..."
ssh "$HOST" "sudo -n systemctl restart kalshi-dashboard"

echo "==> Checking status..."
ssh "$HOST" "sudo -n systemctl status kalshi-ghost-trader --no-pager -n 5"

echo "==> Done. Check logs with:"
echo "    ssh $HOST 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since \"5 min ago\"'"
