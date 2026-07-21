---
name: deploy
description: Deploy the app to production. Use when the user asks to deploy, push to remote, update remote, or ship to mint.
---

# Deploy to Mint

**Always use `deploy/deploy.sh` to deploy. Never run individual SSH commands to build, upload, or restart services on mint.**

```bash
./deploy/deploy.sh mint main
```

This script:
- Builds backend + dashboard locally into `deploy/out/`
- scp's artifacts to remote
- Syncs service file from `deploy/ghost-trader.service`
- Pulls latest code on remote
- Restarts both services
- Runs health check

Do not manually SSH to mint to build, restart, or sync files. Use the script.
