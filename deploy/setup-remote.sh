#!/bin/bash
# First-time setup for ghost-trader on mint box.
# Run on remote: sudo bash setup-remote.sh
set -euo pipefail

REMOTE_DIR="/home/fahad/kalshi-ghost-trader"
SERVICE_SRC="$REMOTE_DIR/deploy/ghost-trader.service"
DASHBOARD_SERVICE_SRC="$REMOTE_DIR/deploy/kalshi-dashboard.service"

echo "==> Installing Go..."
if ! command -v go &>/dev/null; then
  wget -q https://go.dev/dl/go1.24.6.linux-amd64.tar.gz -O /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' >> /home/fahad/.bashrc
  export PATH=$PATH:/usr/local/go/bin
fi

echo "==> Installing sqlite3..."
apt-get update -qq && apt-get install -y -qq sqlite3

echo "==> Installing Node.js..."
if ! command -v node &>/dev/null; then
  curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
  apt-get install -y -qq nodejs
fi

echo "==> Cloning repo..."
if [ ! -d "$REMOTE_DIR" ]; then
  sudo -u fahad git clone https://github.com/LordFarquaadtheCreator/kalshi-ghost-trader.git "$REMOTE_DIR"
fi

echo "==> Installing dashboard deps..."
sudo -u fahad bash -c "cd $REMOTE_DIR/dashboard && npm install"

echo "==> Installing backend service..."
cp "$SERVICE_SRC" /etc/systemd/system/kalshi-ghost-trader.service
systemctl daemon-reload
systemctl enable kalshi-ghost-trader

echo "==> Installing dashboard service..."
if [ -f "$DASHBOARD_SERVICE_SRC" ]; then
  cp "$DASHBOARD_SERVICE_SRC" /etc/systemd/system/kalshi-dashboard.service
  systemctl daemon-reload
  systemctl enable kalshi-dashboard
fi

echo ""
echo "==> Setup complete."
echo "    Repo:     $REMOTE_DIR"
echo "    DB:       $REMOTE_DIR/kalshi_tennis.db (created on first run)"
echo "    Config:   $REMOTE_DIR/app.yaml (create from app.yaml.example)"
echo ""
echo "    Next steps:"
echo "      1. cp app.yaml.example app.yaml && edit credentials"
echo "      2. ./deploy/deploy.sh mint main"
echo ""
