# deploy/

All deployment artifacts for ghost-trader on the Linux Mint box.

## Files

- `deploy.sh` — Build locally, scp artifacts to remote, restart services. Main deploy workflow.
- `setup-remote.sh` — First-time setup. Run on remote with sudo. Installs Go, Node, sqlite3, clones repo, installs systemd services.
- `ghost-trader.service` — systemd unit for backend. Copied to `/etc/systemd/system/` by deploy.sh.
- `kalshi-dashboard.service` — systemd unit for dashboard (if present).
- `out/` — Build output directory (gitignored). Wiped on each deploy.

## Deploy

```bash
./deploy/deploy.sh mint main
```

Builds backend (linux/amd64) + dashboard locally into `deploy/out/`, scp's everything to mint, syncs service file, restarts both services, runs health check.

## First-time setup

```bash
ssh mint 'sudo bash /home/fahad/kalshi-ghost-trader/deploy/setup-remote.sh'
```

Installs Go, Node.js, sqlite3. Clones repo, installs dashboard deps, enables systemd services.

After setup:
1. `cp app.yaml.example app.yaml` and edit credentials
2. `./deploy/deploy.sh mint main`

## Service files

Service files in this dir are the source of truth. `deploy.sh` copies them to `/etc/systemd/system/` on the remote (only if changed). No manual edits on the remote.

- Backend: `ghost-trader.service` — `APP_ENV=prod`, runs as `fahad`
- Dashboard: `kalshi-dashboard.service` — Vite dev server, `BindsTo` backend
