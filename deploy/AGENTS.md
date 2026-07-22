# deploy/

All deployment artifacts for ghost-trader on the Linux Mint box.

## Files

- `deploy.sh` — Build locally, scp artifacts to remote, restart services. Main deploy workflow.
- `setup-remote.sh` — First-time setup. Run on remote with sudo. Installs Go, Node, PostgreSQL, clones repo, installs systemd services.
- `ghost-trader.service` — systemd unit for backend. Copied to `/etc/systemd/system/kalshi-ghost-trader.service` by deploy.sh (only if changed).
- `out/` — Build output directory (gitignored). Wiped on each deploy.

Note: `kalshi-dashboard.service` is NOT in this dir. It lives only on the remote at `/etc/systemd/system/kalshi-dashboard.service` (installed by `setup-remote.sh`). `deploy.sh` restarts it but does not sync a unit file.

## Deploy

```bash
./deploy/deploy.sh mint main
```

Builds backend (linux/amd64) + dashboard locally into `deploy/out/`, scp's everything to mint, syncs service file, restarts both services, runs health check.

## First-time setup

```bash
ssh mint 'sudo bash /home/fahad/kalshi-ghost-trader/deploy/setup-remote.sh'
```

Installs Go, Node.js, PostgreSQL. Clones repo, installs dashboard deps, enables systemd services.

After setup:
1. `cp app.yaml.example app.yaml` and edit credentials
2. `./deploy/deploy.sh mint main`

## Service files

- Backend: `ghost-trader.service` (in this dir) — source of truth. `deploy.sh` syncs to `/etc/systemd/system/kalshi-ghost-trader.service` on remote only if changed. `APP_ENV=prod`, runs as `fahad`.
- Dashboard: `kalshi-dashboard.service` — lives only on remote (installed by `setup-remote.sh`). Vite dev server, `BindsTo` backend. `deploy.sh` restarts it but does not sync the unit file.
