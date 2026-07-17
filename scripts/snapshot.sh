#!/usr/bin/env bash
# Snapshot exporter for ghost-trader DB.
# Runs SQL queries + backtest binary, outputs gzipped JSON to /data/snapshots/.
# Read-only DB access — no interference with writer.
#
# Tiered retention:
#   - All snapshots within 48h
#   - 1/day for 30 days
#   - 1/week for 90 days
#   - Delete older
#
# Usage:
#   /data/snapshot.sh
#   DB_PATH=/data/kalshi_tennis.db /data/snapshot.sh
#
# Cron (every 6 hours):
#   0 */6 * * * /data/snapshot.sh >> /data/snapshots/cron.log 2>&1

set -euo pipefail

DB_PATH="${DB_PATH:-/data/kalshi_tennis.db}"
BINARY_PATH="${BINARY_PATH:-/data/ghost-trader}"
SNAPSHOTS_DIR="${SNAPSHOTS_DIR:-/data/snapshots}"
TIMESTAMP=$(date +%Y%m%d_%H%M)
OUT_DIR="${SNAPSHOTS_DIR}/${TIMESTAMP}"

mkdir -p "$OUT_DIR"

# --- Helper: run SQL, gzip output ---
sql_export() {
  local name="$1"
  local query="$2"
  sqlite3 -json -readonly -bail "$DB_PATH" "$query" 2>/dev/null | gzip > "$OUT_DIR/${name}.json.gz"
  local rows
  rows=$(sqlite3 -readonly "$DB_PATH" "SELECT COUNT(*) FROM (${query})" 2>/dev/null || echo "?")
  echo "  ${name}.json.gz — ${rows} rows"
}

echo "=== Snapshot ${TIMESTAMP} ==="

# --- Orders with P&L ---
sql_export "orders" "
SELECT
  o.ts, o.match_ticker, o.market_ticker, o.action, o.context,
  o.conv_prob, o.market_price, o.edge_cents, o.suggested_size,
  o.set_number, o.strategy,
  m.result,
  CASE
    WHEN o.action = 'buy_no' AND m.result = 'no' THEN 1
    WHEN o.action = 'buy'    AND m.result = 'yes' THEN 1
    ELSE 0
  END AS won,
  CASE
    WHEN (o.action = 'buy_no' AND m.result = 'no')
      OR (o.action = 'buy'    AND m.result = 'yes')
    THEN o.suggested_size * (1.0 - o.market_price)
    ELSE -o.suggested_size * o.market_price
  END AS pnl
FROM orders o
JOIN markets m ON o.market_ticker = m.market_ticker
WHERE m.result IS NOT NULL AND m.result != ''
ORDER BY o.ts"

# --- Unresolved orders ---
sql_export "orders_unresolved" "
SELECT
  o.ts, o.match_ticker, o.market_ticker, o.action, o.context,
  o.conv_prob, o.market_price, o.edge_cents, o.suggested_size,
  o.set_number, o.strategy, m.status, m.result
FROM orders o
JOIN markets m ON o.market_ticker = m.market_ticker
WHERE m.result IS NULL OR m.result = ''
ORDER BY o.ts"

# --- Events summary ---
sql_export "events_summary" "
SELECT
  e.event_ticker, e.series_ticker, e.title, e.sub_title,
  e.competition, e.coverage,
  e.first_seen_ts, e.last_updated_ts,
  (SELECT COUNT(*) FROM markets m WHERE m.event_ticker = e.event_ticker) AS market_count,
  (SELECT GROUP_CONCAT(m.status) FROM markets m WHERE m.event_ticker = e.event_ticker) AS market_statuses,
  (SELECT GROUP_CONCAT(m.result) FROM markets m WHERE m.event_ticker = e.event_ticker) AS market_results,
  (SELECT MAX(m.close_ts) FROM markets m WHERE m.event_ticker = e.event_ticker) AS close_ts,
  (SELECT MAX(m.settlement_ts) FROM markets m WHERE m.event_ticker = e.event_ticker) AS settlement_ts
FROM events e
ORDER BY e.last_updated_ts DESC"

# --- Strategy summary ---
sql_export "strategy_summary" "
SELECT
  o.strategy,
  COUNT(*) AS total_orders,
  SUM(CASE WHEN o.won = 1 THEN 1 ELSE 0 END) AS wins,
  SUM(CASE WHEN o.won = 0 THEN 1 ELSE 0 END) AS losses,
  ROUND(SUM(o.pnl), 4) AS total_pnl,
  ROUND(SUM(o.suggested_size * o.market_price), 4) AS total_invested,
  ROUND(AVG(o.market_price), 4) AS avg_price,
  ROUND(AVG(o.edge_cents), 1) AS avg_edge,
  ROUND(AVG(o.suggested_size), 1) AS avg_size,
  ROUND(SUM(o.pnl) / NULLIF(SUM(o.suggested_size * o.market_price), 0) * 100, 2) AS roi_pct
FROM (
  SELECT o.*,
    CASE WHEN o.action = 'buy_no' AND m.result = 'no' THEN 1
         WHEN o.action = 'buy'    AND m.result = 'yes' THEN 1
         ELSE 0 END AS won,
    CASE WHEN (o.action = 'buy_no' AND m.result = 'no')
           OR (o.action = 'buy'    AND m.result = 'yes')
         THEN o.suggested_size * (1.0 - o.market_price)
         ELSE -o.suggested_size * o.market_price END AS pnl
  FROM orders o
  JOIN markets m ON o.market_ticker = m.market_ticker
  WHERE m.result IS NOT NULL AND m.result != ''
) o
GROUP BY o.strategy
ORDER BY total_pnl DESC"

# --- Tick stats (top 500 by count) ---
sql_export "tick_stats" "
SELECT
  market_ticker,
  COUNT(*) AS tick_count,
  MIN(ts) AS first_ts,
  MAX(ts) AS last_ts,
  ROUND((MAX(ts) - MIN(ts)) / 1000.0, 1) AS span_seconds,
  COUNT(CASE WHEN msg_type = 'trade' THEN 1 END) AS trade_count,
  COUNT(CASE WHEN msg_type = 'ticker' THEN 1 END) AS ticker_count
FROM ticks
GROUP BY market_ticker
ORDER BY tick_count DESC
LIMIT 500"

# --- Scan runs (last 100) ---
sql_export "scan_runs" "
SELECT * FROM scan_runs ORDER BY run_ts DESC LIMIT 100"

# --- Recent lifecycle (last 200) ---
sql_export "lifecycle_summary" "
SELECT
  market_ticker, event_type, result, ts,
  open_ts, close_ts, settled_ts, settlement_value
FROM lifecycle_events
ORDER BY ts DESC
LIMIT 200"

# --- Points summary ---
sql_export "points_summary" "
SELECT
  match_ticker,
  COUNT(*) AS point_count,
  MIN(ts_ms) AS first_ts,
  MAX(ts_ms) AS last_ts,
  SUM(is_match_point) AS match_points,
  SUM(is_break_point) AS break_points,
  SUM(is_set_point) AS set_points
FROM points
WHERE ts_ms IS NOT NULL
GROUP BY match_ticker
ORDER BY point_count DESC"

# --- DB stats ---
{
  echo "{"
  echo "  \"snapshot_ts\": \"${TIMESTAMP}\","
  echo "  \"db_size_bytes\": $(stat -c%s "$DB_PATH" 2>/dev/null || stat -f%z "$DB_PATH" 2>/dev/null || echo 0),"
  for table in events markets ticks orderbook_events lifecycle_events event_lifecycle_events orders fired_events points scan_runs; do
    count=$(sqlite3 -readonly "$DB_PATH" "SELECT COUNT(*) FROM $table" 2>/dev/null || echo -1)
    echo "  \"${table}\": ${count},"
  done
  fs_count=$(sqlite3 -readonly "$DB_PATH" "SELECT COUNT(*) FROM flashscore_matches" 2>/dev/null || echo -1)
  echo "  \"flashscore_matches\": ${fs_count},"
  echo "  \"db_path\": \"${DB_PATH}\""
  echo "}"
} | gzip > "$OUT_DIR/db_stats.json.gz"
echo "  db_stats.json.gz"

# --- Backtest ---
if [ -x "$BINARY_PATH" ]; then
  echo "  Running backtest (all strategies)..."
  "$BINARY_PATH" backtest -strategy all -db "$DB_PATH" 2>/dev/null | gzip > "$OUT_DIR/backtest.txt.gz"
  echo "  backtest.txt.gz"
else
  echo "  backtest skipped — binary not found at $BINARY_PATH"
fi

# --- Meta ---
{
  echo "{"
  echo "  \"snapshot_ts\": \"${TIMESTAMP}\","
  echo "  \"hostname\": \"$(hostname)\","
  echo "  \"uptime_seconds\": $(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0),"
  PID=$(pgrep -f 'ghost-trader' | head -1 || echo "")
  if [ -n "$PID" ]; then
    echo "  \"ghost_trader_pid\": ${PID},"
    echo "  \"ghost_trader_rss_kb\": $(grep VmRSS /proc/$PID/status 2>/dev/null | awk '{print $2}' || echo 0),"
    METRICS=$(curl -s http://127.0.0.1:6060/metrics 2>/dev/null || echo "")
    if [ -n "$METRICS" ]; then
      GOROUTINES=$(echo "$METRICS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('goroutines',''))" 2>/dev/null || echo "")
      HEAP_MB=$(echo "$METRICS" | python3 -c "import sys,json; print(round(json.load(sys.stdin).get('heap_alloc_bytes',0)/1048576,1))" 2>/dev/null || echo "")
      echo "  \"goroutines\": ${GOROUTINES:-0},"
      echo "  \"heap_mb\": ${HEAP_MB:-0},"
    fi
  fi
  echo "  \"created_at\": \"$(date -Iseconds)\""
  echo "}"
} | gzip > "$OUT_DIR/meta.json.gz"
echo "  meta.json.gz"

# --- Tiered retention cleanup ---
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

  while IFS= read -r snap; do
    [ -z "$snap" ] && continue
    local name
    name=$(basename "$snap")
    local date_part="${name%%_*}"

    local snap_epoch
    snap_epoch=$(date -d "$date_part" +%s 2>/dev/null || echo 0)
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
      year_week=$(date -d "$date_part" +%Y%U 2>/dev/null || echo "0")
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

cleanup_tiered "$SNAPSHOTS_DIR"

echo "=== Snapshot complete: $OUT_DIR ==="
echo "  $(du -sh "$OUT_DIR" | cut -f1) total"
