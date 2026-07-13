# Oracle Cloud Deployment — Step by Step

## Phase 1: Oracle Cloud Account

### 1.1 Sign up
- Go to cloud.oracle.com
- Home region: **US East (Ashburn)** — `us-ashburn-1`
  - This is permanent. Cannot change later.
  - Best for Kalshi latency (CloudFront edge + 30ms to engine)
- Use real credit card (not debit, not prepaid, not virtual cards like Privacy.com)
- No VPN/proxy during signup — Oracle fraud detection flags non-residential IPs
- Address must match card billing address exactly
- One account per person — multiple accounts = permanent ban
- If rejected: try again from different browser/device, no VPN, real credit card

### 1.2 Upgrade to Pay As You Go
- Do this immediately after signup
- Console → Account Management → Upgrade
- Still $0 within Always Free limits
- Removes idle instance reclamation policy (the main risk)
- You only pay if you exceed Always Free resource limits
- Set a billing alert at $1 just in case

### 1.3 Verify
- [ ] Can log into Console
- [ ] Home region shows `us-ashburn-1`
- [ ] Account shows Pay As You Go status

---

## Phase 2: OCI API Authentication

The MCP server + CLI read from `~/.oci/config`. Set this up first.

### 2.1 Generate API keypair
```bash
mkdir -p ~/.oci
openssl genrsa -out ~/.oci/oci_api_key.pem 2048
chmod 600 ~/.oci/oci_api_key.pem
openssl rsa -pubout -in ~/.oci/oci_api_key.pem -out ~/.oci/oci_api_key_public.pem
```

### 2.2 Get credentials from Console
- **Tenancy OCID**: Console → Identity → Tenancy → copy OCID (starts with `ocid1.tenancy.oc1..`)
- **User OCID**: Console → Profile → My profile → copy OCID (starts with `ocid1.user.oc1..`)
- **Region**: `us-ashburn-1`

### 2.3 Upload public key to Console
- Console → Profile → API Keys → Add API key
- Select "Paste public key"
- Paste contents of `~/.oci/oci_api_key_public.pem`
- Console shows fingerprint (copy it)

### 2.4 Write config file
Create `~/.oci/config`:
```ini
[DEFAULT]
user=ocid1.user.oc1..REPLACE_WITH_YOUR_USER_OCID
fingerprint=REPLACE_WITH_FINGERPRINT_FROM_CONSOLE
tenancy=ocid1.tenancy.oc1..REPLACE_WITH_YOUR_TENANCY_OCID
region=us-ashburn-1
key_file=~/.oci/oci_api_key.pem
```

```bash
chmod 600 ~/.oci/config
```

### 2.5 Verify
```bash
# If OCI CLI installed:
oci iam region list

# Or test with Python SDK:
python3 -c "import oci; c = oci.config.from_file(); print(c['tenancy'])"
```

- [ ] `~/.oci/config` exists with all 5 fields
- [ ] `~/.oci/oci_api_key.pem` exists, permissions 600
- [ ] API key shows in Console → Profile → API Keys
- [ ] `oci iam region list` returns successfully (if CLI installed)

---

## Phase 3: OCI MCP Server

Already registered in `~/.config/devin/mcp_config.json` as `oci` server.
Need to restart Devin to pick it up.

### 3.1 Verify MCP config
File: `~/.config/devin/mcp_config.json`
```json
{
  "mcpServers": {
    "oci": {
      "command": "uvx",
      "args": ["oracle.oci-cloud-mcp-server"]
    }
  }
}
```

### 3.2 Restart Devin
- Quit Devin CLI
- Restart
- MCP server `oci` should appear in server list

### 3.3 Verify
- [ ] Devin restarts without errors
- [ ] `oci` MCP server shows in `mcp_list_servers`
- [ ] `mcp_list_tools` for `oci` returns tool list

---

## Phase 4: Create Compute Instance

### 4.1 Create ARM instance (preferred)
- Shape: `VM.Standard.A1.Flex`
- OCPUs: 2
- Memory: 4 GB
- OS: Ubuntu 24.04 LTS (ARM64)
- Boot volume: 50 GB (default)
- SSH key: upload your public key (`~/.ssh/id_rsa.pub` or similar)
- Availability domain: any (try AD-1 first)

### 4.2 If "Out of host capacity"
This is common for ARM. Options:
- Retry every few hours via Console
- Use OCI CLI retry loop (after Phase 2 auth is set up):
```bash
# Get these from Console or CLI:
# compartment-id, subnet-id, image-ocid
while true; do
  oci compute instance launch \
    --shape VM.Standard.A1.Flex \
    --shape-config "{\"ocpus\":2,\"memoryInGBs\":4}" \
    --availability-domain "TGTh:US-EAST-ASHBURN-1-AD-1" \
    --compartment-id "<compartment-ocid>" \
    --image-ocid "<ubuntu-2404-arm64-image-ocid>" \
    --subnet-id "<subnet-ocid>" \
    --assign-public-ip true \
    --ssh-authorized-keys-file ~/.ssh/id_rsa.pub \
  && break
  echo "Retrying in 60s..."
  sleep 60
done
```

### 4.3 Fallback: AMD micro instance (if ARM unavailable)
- Shape: `VM.Standard.E2.1.Micro`
- 1/8 OCPU, 1 GB RAM
- Always available
- Deploy app here while waiting for ARM capacity
- Can run both simultaneously (different quotas)

### 4.4 Verify
- [ ] Instance shows "Running" in Console
- [ ] Can SSH in: `ssh ubuntu@<instance-public-ip>`
- [ ] `uname -m` shows `aarch64` (ARM) or `x86_64` (AMD)

---

## Phase 5: Create + Attach Block Volume

### 5.1 Create block volume
- Console → Block Storage → Block Volumes → Create
- Name: `kalshi-data`
- Size: 50 GB
- Performance: Balanced (default, free)
- Availability domain: same as instance

### 5.2 Attach to instance
- Select volume → Attach to instance
- Attachment type: Paravirtualized (auto-connects, no iSCSI setup)
- Access: Read/Write

### 5.3 Format + mount (on instance)
```bash
ssh ubuntu@<instance-ip>

# Find device
lsblk
# Look for /dev/sdb (or similar, not sda which is boot)

# Format
sudo mkfs.ext4 /dev/sdb

# Create mount point
sudo mkdir /data

# Mount
sudo mount /dev/sdb /data

# Auto-mount on boot
UUID=$(sudo blkid -s UUID -o value /dev/sdb)
echo "UUID=$UUID /data ext4 defaults,_netdev,nofail 0 2" | sudo tee -a /etc/fstab

# Set ownership
sudo chown ubuntu:ubuntu /data

# Verify
df -h /data
```

### 5.4 Verify
- [ ] Volume shows "Attached" in Console
- [ ] `df -h /data` shows 50GB
- [ ] `mount | grep /data` shows mounted
- [ ] Reboot test: `sudo reboot`, SSH back in, `df -h /data` still shows mounted

---

## Phase 6: Deploy App

### 6.1 Build binary (on your Mac)
```bash
cd /Users/farquaad/kalshi-ghost-trader
./deploy/build.sh
# Output: deploy/out/ghost-trader (ARM64 Linux binary)
```

### 6.2 Run deploy script
```bash
./deploy/deploy.sh <instance-ip>
# This uploads binary + service file + runs remote setup
# Remote setup: mounts volume, installs binary, configures systemd
```

### 6.3 Upload credentials
```bash
scp .env ubuntu@<instance-ip>:/data/.env
scp ~/.ssh/kalshi_key.pem ubuntu@<instance-ip>:/data/kalshi_key.pem
```

### 6.4 Fix paths in .env (on instance)
```bash
ssh ubuntu@<instance-ip>
sed -i 's|KALSHI_PRIVATE_KEY_PATH=.*|KALSHI_PRIVATE_KEY_PATH=/data/kalshi_key.pem|' /data/.env
sed -i 's|DB_PATH=.*|DB_PATH=/data/kalshi_tennis.db|' /data/.env
chmod 600 /data/.env /data/kalshi_key.pem
```

### 6.5 Start service
```bash
sudo systemctl start ghost-trader
sudo journalctl -u ghost-trader -f
```

### 6.6 Verify
- [ ] Binary at `/data/ghost-trader`, executable
- [ ] `.env` at `/data/.env`, permissions 600
- [ ] Private key at `/data/kalshi_key.pem`, permissions 600
- [ ] `systemctl status ghost-trader` shows active
- [ ] Logs show WS connection established
- [ ] Logs show scanner running
- [ ] `ls /data/kalshi_tennis.db` exists after first run

---

## Phase 7: Backup Setup (optional but recommended)

### 7.1 Create backup script
```bash
ssh ubuntu@<instance-ip>
sudo mkdir -p /data/backups
sudo chown ubuntu:ubuntu /data/backups

echo '#!/bin/bash
sqlite3 /data/kalshi_tennis.db ".backup '\''/data/backups/kalshi_$(date +%Y%m%d).db'\''"
find /data/backups/ -name "kalshi_*.db" -mtime +7 -delete' | sudo tee /etc/cron.daily/backup-kalshi-db
sudo chmod +x /etc/cron.daily/backup-kalshi-db
```

### 7.2 Install sqlite3 CLI (needed for backup)
```bash
sudo apt update && sudo apt install -y sqlite3
```

### 7.3 Verify
- [ ] `sqlite3 --version` works
- [ ] `/etc/cron.daily/backup-kalshi-db` exists, executable
- [ ] Manual test: `sudo /etc/cron.daily/backup-kalshi-db && ls /data/backups/`

---

## Phase 8: Migrate to ARM (if started on AMD)

If you deployed on AMD micro while waiting for ARM capacity:

### 8.1 Stop service on AMD
```bash
ssh ubuntu@<amd-instance-ip>
sudo systemctl stop ghost-trader
```

### 8.2 Detach block volume from AMD instance
- Console → Block Storage → kalshi-data → Detach

### 8.3 Attach to ARM instance
- Console → Block Storage → kalshi-data → Attach to ARM instance
- Paravirtualized, Read/Write

### 8.4 Mount on ARM instance
```bash
ssh ubuntu@<arm-instance-ip>
sudo mkdir -p /data
sudo mount /dev/sdb /data
# Add to fstab if not already there
UUID=$(sudo blkid -s UUID -o value /dev/sdb)
echo "UUID=$UUID /data ext4 defaults,_netdev,nofail 0 2" | sudo tee -a /etc/fstab
sudo chown ubuntu:ubuntu /data
```

### 8.5 Deploy + start
```bash
./deploy/deploy.sh <arm-instance-ip>
ssh ubuntu@<arm-instance-ip>
sudo systemctl start ghost-trader
sudo journalctl -u ghost-trader -f
```

### 8.6 Verify
- [ ] ARM instance running, service active
- [ ] SQLite DB intact: `sqlite3 /data/kalshi_tennis.db "SELECT COUNT(*) FROM ticks;"`
- [ ] AMD instance can be terminated

---

## Quick Reference

### Files on your Mac
| File | Purpose |
|------|---------|
| `~/.oci/config` | OCI API auth config |
| `~/.oci/oci_api_key.pem` | OCI API private key |
| `~/.config/devin/mcp_config.json` | Devin MCP config (includes OCI server) |
| `deploy/build.sh` | Cross-compile to ARM64 |
| `deploy/deploy.sh` | Upload + install to instance |
| `deploy/setup-remote.sh` | Runs on instance: mount, install, systemd |
| `deploy/ghost-trader.service` | Systemd unit file |
| `deploy/out/ghost-trader` | Built binary (ARM64) |

### Files on instance
| File | Purpose |
|------|---------|
| `/data/ghost-trader` | Binary |
| `/data/.env` | Config (env vars) |
| `/data/kalshi_key.pem` | Kalshi RSA private key |
| `/data/kalshi_tennis.db` | SQLite database |
| `/data/backups/` | Daily DB backups |
| `/etc/systemd/system/ghost-trader.service` | Systemd unit |

### Useful commands
```bash
# Build
./deploy/build.sh

# Deploy
./deploy/deploy.sh <ip>

# Logs (live)
ssh ubuntu@<ip> 'sudo journalctl -u ghost-trader -f'

# Status
ssh ubuntu@<ip> 'sudo systemctl status ghost-trader'

# Restart
ssh ubuntu@<ip> 'sudo systemctl restart ghost-trader'

# Stop
ssh ubuntu@<ip> 'sudo systemctl stop ghost-trader'

# Check DB size
ssh ubuntu@<ip> 'ls -lh /data/kalshi_tennis.db'

# Row counts
ssh ubuntu@<ip> 'sqlite3 /data/kalshi_tennis.db "SELECT COUNT(*) FROM ticks; SELECT COUNT(*) FROM markets; SELECT COUNT(*) FROM events;"'
```

### Cost
$0/month. Within Always Free limits:
- 2 OCPU + 4GB RAM ARM (of 4 OCPU + 24GB free)
- 50GB boot + 50GB data = 100GB (of 200GB free)
- 10TB/month outbound (app uses <1GB/month)
