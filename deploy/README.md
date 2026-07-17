# Remote Deployment

## Prerequisites

1. Remote ARM instance (e.g. Ampere A1, 2 OCPU, 4GB RAM, Ubuntu 24.04 LTS)
2. Create 50GB block volume, attach to instance
3. SSH key: upload your public key during instance creation

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

Daily full DB backup, keep 7 days:

```bash
echo '#!/bin/bash
sqlite3 /data/kalshi_tennis.db ".backup '\''/data/backups/kalshi_$(date +%Y%m%d).db'\''"
find /data/backups/ -name "kalshi_*.db" -mtime +7 -delete' | sudo tee /etc/cron.daily/backup-kalshi-db
sudo chmod +x /etc/cron.daily/backup-kalshi-db
sudo mkdir -p /data/backups
sudo chown ubuntu:ubuntu /data/backups
```

## Snapshots

Summarized exports for local analysis. Runs SQL queries + backtest binary on
remote, outputs gzipped JSON to `/data/snapshots/YYYYMMDD_HHMM/`.

```bash
# Upload snapshot script
scp scripts/snapshot.sh ubuntu@<ip>:/data/snapshot.sh
ssh ubuntu@<ip> 'chmod +x /data/snapshot.sh && mkdir -p /data/snapshots'

# Add to crontab (every 6 hours)
ssh ubuntu@<ip> 'echo "0 */6 * * * /data/snapshot.sh >> /data/snapshots/cron.log 2>&1" | crontab -'

# Test manually
ssh ubuntu@<ip> '/data/snapshot.sh'

# Fetch locally
./scripts/fetch-snapshots.sh <ip>
```

Tiered retention (both remote + local):
- 0–48h: keep all (8 at 6h intervals)
- 2–30 days: keep 1 per day
- 30–90 days: keep 1 per week
- 90+ days: delete

Exports: `orders.json.gz` (P&L), `strategy_summary.json.gz`, `events_summary.json.gz`,
`tick_stats.json.gz`, `backtest.txt.gz`, `db_stats.json.gz`, `meta.json.gz`, others.

## Cost

Depends on hosting provider. Within free tier limits on most providers:
- 2 OCPU + 4GB RAM ARM
- 50GB boot + 50GB data = 100GB
- Minimal outbound traffic
