#!/bin/bash
# Runs on the Oracle Cloud instance. Sets up /data volume, installs binary,
# configures systemd. Run via deploy.sh (SSH).
set -e

DATA_DIR="/data"
BINARY="/tmp/ghost-trader"
SERVICE_FILE="/tmp/ghost-trader.service"

# --- Mount data volume if not mounted ---
if ! mountpoint -q "$DATA_DIR"; then
  echo "==> Formatting and mounting data volume..."
  mkdir -p "$DATA_DIR"

  # Find unformatted block device (skip sda = boot)
  DEVICE=$(lsblk -d -n -o NAME | grep -v '^sda$' | grep '^sd' | head -1)
  if [ -z "$DEVICE" ]; then
    DEVICE=$(lsblk -d -n -o NAME | grep -v '^sda$' | grep -v '^loop' | head -1)
  fi

  if [ -z "$DEVICE" ]; then
    echo "ERROR: No block device found for data volume."
    echo "Create + attach a block volume in Oracle Console first."
    exit 1
  fi

  echo "==> Found device: /dev/$DEVICE"
  mkfs.ext4 -F "/dev/$DEVICE"
  mount "/dev/$DEVICE" "$DATA_DIR"

  # Auto-mount on boot
  UUID=$(blkid -s UUID -o value "/dev/$DEVICE")
  echo "UUID=$UUID $DATA_DIR ext4 defaults,_netdev,nofail 0 2" >> /etc/fstab
  echo "==> Added fstab entry for UUID=$UUID"
fi

# --- Set ownership ---
chown ubuntu:ubuntu "$DATA_DIR"
chmod 755 "$DATA_DIR"

# --- Install binary ---
echo "==> Installing binary..."
cp "$BINARY" "$DATA_DIR/ghost-trader"
chmod +x "$DATA_DIR/ghost-trader"

# --- Install systemd service ---
echo "==> Installing systemd service..."
cp "$SERVICE_FILE" /etc/systemd/system/ghost-trader.service
systemctl daemon-reload
systemctl enable ghost-trader

# --- Check for .env ---
if [ ! -f "$DATA_DIR/.env" ]; then
  echo ""
  echo "==> WARNING: $DATA_DIR/.env not found!"
  echo "    Upload it manually:"
  echo "    scp .env ubuntu@<ip>:/data/.env"
  echo "    scp <private-key.pem> ubuntu@<ip>:/data/kalshi_key.pem"
  echo "    Then edit .env to set KALSHI_PRIVATE_KEY_PATH=/data/kalshi_key.pem"
  echo "    Then: sudo systemctl start ghost-trader"
  echo ""
else
  echo "==> .env found. Starting service..."
  systemctl restart ghost-trader
  sleep 2
  systemctl status ghost-trader --no-pager || true
fi

echo ""
echo "==> Setup complete."
echo "    Data dir: $DATA_DIR"
echo "    Binary:   $DATA_DIR/ghost-trader"
echo "    Config:   $DATA_DIR/.env"
echo "    DB:       $DATA_DIR/kalshi_tennis.db (created on first run)"
echo ""
echo "    Logs:   sudo journalctl -u ghost-trader -f"
echo "    Stop:   sudo systemctl stop ghost-trader"
echo "    Start:  sudo systemctl start ghost-trader"
echo "    Restart: sudo systemctl restart ghost-trader"
