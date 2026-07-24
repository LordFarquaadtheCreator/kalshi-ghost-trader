---
name: deploy
description: Deploy kalshi-ghost-trader to the Linux Mint box. Use when the user asks to deploy, push to remote, update remote, or ship to mint.
---

# Deploy to Mint

**Always use `deploy/deploy.sh`. Never run individual SSH commands to build, upload, or restart services on mint.**

```bash
./deploy/deploy.sh mint main              # both backend + dashboard (default)
./deploy/deploy.sh mint main --backend    # backend only
./deploy/deploy.sh mint main --dashboard  # dashboard only
```

## Prerequisites

- SSH alias `mint` in `~/.ssh/config` (host: `linux-mint`, user: `fahad`, IP: `192.168.1.246`)
- Passwordless sudo for `systemctl` + `journalctl` on mint
- Repo at `/home/fahad/kalshi-ghost-trader` on mint
- Local tree clean and pushed to `origin/main` — deploy.sh hard-fails otherwise

## What deploy.sh does

Flags: `--backend` (build + upload binaries, sync service file, restart backend), `--dashboard` (rsync dashboard source, restart dashboard). Omit both = deploy everything.

1. Builds backend + backtest locally (linux/amd64) into `deploy/out/` — skipped with `--dashboard`
2. scp's `ghost-trader` binary to remote repo root `/home/fahad/kalshi-ghost-trader/ghost-trader` — skipped with `--dashboard`
3. scp's `backtest` binary to `/home/fahad/kalshi-ghost-trader/bin/backtest` — skipped with `--dashboard`
4. Syncs `deploy/ghost-trader.service` to `/etc/systemd/system/` on remote (only if changed, then `daemon-reload`) — skipped with `--dashboard`
5. `git fetch + checkout + reset --hard` remote to local HEAD — always runs
6. rsyncs `dashboard/` source to remote (excludes node_modules, build, .svelte-kit) — skipped with `--backend`
7. Restarts services selected by flags
8. Health check: `curl 127.0.0.1:6060/metrics` (expect 200) + `curl 127.0.0.1:5173/` (expect 307 — Vite proxy redirect, healthy)

## Binary location

Backend binary lives at **repo root** `/home/fahad/kalshi-ghost-trader/ghost-trader` — that's where `kalshi-ghost-trader.service` `ExecStart` points. NOT `bin/ghost-trader` (that's local-only convention from AGENTS.md). deploy.sh scp's to repo root. Do not build to `bin/` on mint.

Dashboard has no binary — Vite reads source from `dashboard/`, rsynced by deploy.sh.

## Uncommitted local changes

deploy.sh requires clean tree. If Fahad says ignore uncommitted changes:

```bash
git stash push -u -m "pre-deploy-uncommitted" && ./deploy/deploy.sh mint main; RC=$?; git stash pop; exit $RC
```

## Service files

- **Backend**: `deploy/ghost-trader.service` — source of truth. deploy.sh syncs to remote only if changed.
- **Dashboard**: `kalshi-dashboard.service` — lives only on remote (installed by `setup-remote.sh`). deploy.sh restarts it but does not sync the unit file.

## Schema changes

Migrations auto-run on startup via embedded SQL in `internal/store/migrations/`. No manual step for additive changes. For new config tables, check `cmd/migrate-config`:

```bash
ssh mint 'cd /home/fahad/kalshi-ghost-trader && go run ./cmd/migrate-config'
```

## Verify after deploy

```bash
ssh mint 'sudo -n systemctl status kalshi-ghost-trader --no-pager -n 5 && echo "---" && sudo -n systemctl status kalshi-dashboard --no-pager -n 5'
```

Both `active (running)`. Backend 200 on `/metrics`, dashboard 307 on `/`.

## Troubleshooting

- **Build failure**: Run `go build ./...` locally first. Usually missing import or type mismatch.
- **Dashboard proxy errors on startup**: Normal — Vite starts faster than backend. Resolves once backend up.
- **Service won't start**: `ssh mint 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 40 --since "5 min ago"'`
- **Binary path mismatch**: If service runs stale binary, check `ExecStart` in unit file points at repo root `ghost-trader`, not `bin/ghost-trader`.
