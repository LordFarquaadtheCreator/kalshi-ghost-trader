#!/usr/bin/env bash
# Fetch snapshot exports from remote ghost-trader instance.
# rsyncs gzipped JSON + backtest output from remote to local.
#
# Tiered retention (same as remote):
#   - All snapshots within 48h
#   - 1/day for 30 days
#   - 1/week for 90 days
#   - Delete older
#
# Usage:
#   ./scripts/fetch-snapshots.sh <instance-ip>
#   ./scripts/fetch-snapshots.sh 129.146.42.10
#   REMOTE_DIR=/data/snapshots ./scripts/fetch-snapshots.sh <ip>

set -euo pipefail

INSTANCE_IP="${1:?Usage: $0 <instance-ip>}"
REMOTE_DIR="${REMOTE_DIR:-/data/snapshots}"
LOCAL_DIR="${LOCAL_DIR:-$(dirname "$0")/../snapshots}"

mkdir -p "$LOCAL_DIR"

echo "==> Fetching snapshots from ${INSTANCE_IP}:${REMOTE_DIR}"
rsync -avz --progress \
  --partial \
  "ubuntu@${INSTANCE_IP}:${REMOTE_DIR}/" \
  "$LOCAL_DIR/"

# --- Tiered retention cleanup (macOS compatible) ---
cleanup_tiered() {
  local dir="$1"
  local now_epoch
  now_epoch=$(date +%s)

  local full_window=$((48 * 3600))
  local daily_window=$((30 * 86400))
  local weekly_window=$((90 * 86400))

  local kept_full=0 kept_daily=0 kept_weekly=0 deleted=0
  declare -A kept_days
  declare -A kept_weeks

  # macOS date uses -j -f, Linux uses -d
  date_to_epoch() {
    local d="$1"
    if [[ "$(uname)" == "Darwin" ]]; then
      date -j -f "%Y%m%d" "$d" +%s 2>/dev/null || echo 0
    else
      date -d "$d" +%s 2>/dev/null || echo 0
    fi
  }

  date_to_yearweek() {
    local d="$1"
    if [[ "$(uname)" == "Darwin" ]]; then
      date -j -f "%Y%m%d" "$d" +%Y%U 2>/dev/null || echo "0"
    else
      date -d "$d" +%Y%U 2>/dev/null || echo "0"
    fi
  }

  while IFS= read -r snap; do
    [ -z "$snap" ] && continue
    local name
    name=$(basename "$snap")
    local date_part="${name%%_*}"

    local snap_epoch
    snap_epoch=$(date_to_epoch "$date_part")
    [ "$snap_epoch" -eq 0 ] && continue

    local age=$((now_epoch - snap_epoch))

    if [ "$age" -le "$full_window" ]; then
      kept_full=$((kept_full + 1))
    elif [ "$age" -le "$daily_window" ]; then
      if [ -z "${kept_days[$date_part]:-}" ]; then
        kept_days[$date_part]=1
        kept_daily=$((kept_daily + 1))
      else
        rm -rf "$snap"
        deleted=$((deleted + 1))
      fi
    elif [ "$age" -le "$weekly_window" ]; then
      local year_week
      year_week=$(date_to_yearweek "$date_part")
      if [ -z "${kept_weeks[$year_week]:-}" ]; then
        kept_weeks[$year_week]=1
        kept_weekly=$((kept_weekly + 1))
      else
        rm -rf "$snap"
        deleted=$((deleted + 1))
      fi
    else
      rm -rf "$snap"
      deleted=$((deleted + 1))
    fi
  done < <(ls -1d "${dir}"/*/ 2>/dev/null | sort -r)

  echo "  Retention: ${kept_full} recent, ${kept_daily} daily, ${kept_weekly} weekly, ${deleted} deleted"
}

echo "==> Applying tiered retention"
cleanup_tiered "$LOCAL_DIR"

echo "==> Done. Snapshots in $LOCAL_DIR"
echo "    Latest: $(ls -1d "$LOCAL_DIR"/*/ 2>/dev/null | sort -r | head -1)"
echo ""
echo "To inspect:"
echo "  zcat snapshots/<dir>/orders.json.gz | python3 -m json.tool | head"
echo "  zcat snapshots/<dir>/strategy_summary.json.gz | python3 -m json.tool"
echo "  zcat snapshots/<dir>/backtest.txt.gz"
