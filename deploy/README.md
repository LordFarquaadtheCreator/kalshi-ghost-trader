# Oracle Cloud Deployment

## Prerequisites

1. Oracle Cloud account (signup at cloud.oracle.com)
   - Home region: **US East (Ashburn)** — `us-ashburn-1`
   - Real credit card, no VPN, address matches card
2. Upgrade to **Pay As You Go** after signup
   - Still $0 within Always Free limits
   - Removes idle instance reclamation
3. Create ARM instance: `VM.Standard.A1.Flex`
   - 2 OCPU, 4GB RAM, Ubuntu 24.04 LTS
   - If "Out of capacity" — retry. See retry script below.
4. Create 50GB block volume, attach to instance
5. SSH key: upload your public key during instance creation

## Deploy

```bash
# 1. Build binary for ARM64
./deploy/build.sh

# 2. Deploy to instance
./deploy/deploy.sh <instance-ip>

# 3. Upload credentials (first time only)
scp .env ubuntu@<ip>:/data/.env
scp ~/.ssh/kalshi_key.pem ubuntu@<ip>:/data/kalshi_key.pem

# 4. SSH in, fix key path in .env
ssh ubuntu@<ip>
sed -i 's|KALSHI_PRIVATE_KEY_PATH=.*|KALSHI_PRIVATE_KEY_PATH=/data/kalshi_key.pem|' /data/.env
sed -i 's|DB_PATH=.*|DB_PATH=/data/kalshi_tennis.db|' /data/.env
sudo systemctl start ghost-trader
sudo journalctl -u ghost-trader -f
```

## ARM Capacity Retry

If instance creation fails with "Out of host capacity", retry:

```bash
# Run locally with OCI CLI installed + configured
# Adjust shape config + image OCID + compartment OCID
while true; do
  oci compute instance launch \
    --shape VM.Standard.A1.Flex \
    --shape-config "{\"ocpus\":2,\"memoryInGBs\":4}" \
    --availability-domain "TGTh:US-EAST-ASHBURN-1-AD-1" \
    --compartment-id "<your-compartment-ocid>" \
    --image-ocid "<ubuntu-2404-arm64-image-ocid>" \
    --subnet-id "<your-subnet-ocid>" \
    --assign-public-ip true \
    --ssh-authorized-keys-file ~/.ssh/id_rsa.pub \
  && break
  echo "Retrying in 60s..."
  sleep 60
done
```

Or just click "Create" in the console every few hours.

## Architecture

```
/dev/sda (boot, 50GB)
  /                    OS
  /home/ubuntu/        SSH keys

/dev/sdb (data volume, 50GB, mounted at /data)
  /data/
    ghost-trader           binary
    .env                   config
    kalshi_key.pem         RSA private key
    kalshi_tennis.db       SQLite database
    kalshi_tennis.db-wal   WAL file (auto)
    kalshi_tennis.db-shm   shared memory (auto)
```

## Management

```bash
# Logs (live)
sudo journalctl -u ghost-trader -f

# Status
sudo systemctl status ghost-trader

# Restart
sudo systemctl restart ghost-trader

# Stop
sudo systemctl stop ghost-trader

# Update binary (after code changes)
./deploy/build.sh
./deploy/deploy.sh <ip>
# deploy.sh restarts the service automatically
```

## Backup

```bash
# Add to crontab on instance — daily backup, keep 7 days
echo '#!/bin/bash
sqlite3 /data/kalshi_tennis.db ".backup '\''/data/backups/kalshi_$(date +%Y%m%d).db'\''"
find /data/backups/ -name "kalshi_*.db" -mtime +7 -delete' | sudo tee /etc/cron.daily/backup-kalshi-db
sudo chmod +x /etc/cron.daily/backup-kalshi-db
sudo mkdir -p /data/backups
sudo chown ubuntu:ubuntu /data/backups
```

## Cost

$0/month. Within Always Free limits:
- 2 OCPU + 4GB RAM ARM (of 4 OCPU + 24GB free)
- 50GB boot + 50GB data = 100GB (of 200GB free)
- 10TB/month outbound (app uses <1GB/month)
