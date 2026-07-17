#!/bin/bash
# Deploy ghost-trader to remote instance.
# Usage: ./deploy/deploy.sh <instance-ip>
# Prerequisites: ARM instance running, 50GB block volume mounted at /data
set -e

INSTANCE_IP="$1"
if [ -z "$INSTANCE_IP" ]; then
  echo "Usage: $0 <instance-ip>"
  echo "Example: $0 129.146.42.10"
  exit 1
fi

cd "$(dirname "$0")/.."

echo "==> Building binary..."
./deploy/build.sh

echo "==> Uploading to $INSTANCE_IP..."
scp deploy/out/ghost-trader ubuntu@$INSTANCE_IP:/tmp/ghost-trader
scp deploy/ghost-trader.service ubuntu@$INSTANCE_IP:/tmp/ghost-trader.service
scp deploy/setup-remote.sh ubuntu@$INSTANCE_IP:/tmp/setup-remote.sh

echo "==> Running remote setup..."
ssh ubuntu@$INSTANCE_IP 'chmod +x /tmp/setup-remote.sh && sudo /tmp/setup-remote.sh'

echo "==> Done. Check logs with:"
echo "    ssh ubuntu@$INSTANCE_IP 'sudo journalctl -u ghost-trader -f'"
