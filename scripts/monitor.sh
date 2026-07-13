#!/usr/bin/env bash
# Resource monitor for ghost-trader process.
# Samples CPU%, RSS, network IO, and Go runtime metrics at intervals.
# Logs to CSV. Ctrl-C to stop.
#
# Usage:
#   ./scripts/monitor.sh [pid] [interval_sec] [output_csv]
#   ./scripts/monitor.sh $(pgrep -f ghost-trader) 2 metrics.csv
#
# If pid omitted, auto-detects ghost-trader process.
#
# Output columns:
#   timestamp, pid, cpu_pct, rss_mb, vsz_mb, threads,
#   net_rx_bytes, net_tx_bytes, goroutines, heap_mb, gc_num

set -euo pipefail

PID="${1:-$(pgrep -f 'ghost-trader' | head -1)}"
INTERVAL="${2:-2}"
OUTPUT="${3:-monitor_$(date +%Y%m%d_%H%M%S).csv}"
METRICS_PORT="${METRICS_PORT:-6060}"

if [[ -z "$PID" ]]; then
    echo "Error: no ghost-trader process found. Pass pid as first arg." >&2
    exit 1
fi

if ! kill -0 "$PID" 2>/dev/null; then
    echo "Error: pid $PID not running." >&2
    exit 1
fi

OS="$(uname)"
if [[ "$OS" == "Darwin" ]]; then
    MACOS=1
else
    MACOS=0
fi

echo "Monitoring pid $PID every ${INTERVAL}s. Output: $OUTPUT"
echo "Metrics endpoint: http://127.0.0.1:${METRICS_PORT}/metrics"
echo "timestamp,pid,cpu_pct,rss_mb,vsz_mb,threads,net_rx_bytes,net_tx_bytes,goroutines,heap_mb,gc_num" > "$OUTPUT"

PREV_RX=0
PREV_TX=0

get_net_stats() {
    local rx=0 tx=0
    if [[ "$MACOS" -eq 1 ]]; then
        # macOS: only Link# entries have stable columns (Address field empty)
        # $6=Ibytes, $9=Obytes for Link# lines
        local stats
        stats=$(netstat -ib 2>/dev/null | awk '/Link#/{rx+=$6; tx+=$9} END{print rx, tx}')
        rx=$(echo "$stats" | awk '{print $1}')
        tx=$(echo "$stats" | awk '{print $2}')
    else
        local line
        while IFS= read -r line; do
            local iface rx_bytes tx_bytes
            iface=$(echo "$line" | awk -F: '{print $1}' | tr -d ' ')
            if [[ -n "$iface" && "$iface" != "lo" ]]; then
                rx_bytes=$(echo "$line" | awk '{print $2}')
                tx_bytes=$(echo "$line" | awk '{print $10}')
                rx=$((rx + rx_bytes))
                tx=$((tx + tx_bytes))
            fi
        done < <(cat /proc/net/dev 2>/dev/null | tail -n +3)
    fi
    echo "$rx $tx"
}

get_runtime_metrics() {
    curl -s "http://127.0.0.1:${METRICS_PORT}/metrics" 2>/dev/null || echo ""
}

while true; do
    NOW=$(date +%Y-%m-%dT%H:%M:%S)

    if [[ "$MACOS" -eq 1 ]]; then
        # macOS: %cpu is instantaneous, rss in KB, vsz in KB
        # No thread count keyword in ps. Leave empty — goroutines from metrics more useful.
        CPU_PCT=$(ps -p "$PID" -o %cpu= 2>/dev/null | tr -d ' ' || echo "0")
        RSS_KB=$(ps -p "$PID" -o rss= 2>/dev/null | tr -d ' ' || echo "0")
        VSZ_KB=$(ps -p "$PID" -o vsz= 2>/dev/null | tr -d ' ' || echo "0")
        THREADS=""
    else
        if [[ -f "/proc/$PID/stat" ]]; then
            STAT=$(cat "/proc/$PID/stat")
            UTIME=$(echo "$STAT" | awk '{print $14}')
            STIME=$(echo "$STAT" | awk '{print $15}')
            RSS_KB=$(echo "$STAT" | awk '{print $24}')
            VSZ_KB=$(echo "$STAT" | awk '{print $23}')
            THREADS=$(echo "$STAT" | awk '{print $20}')
            # CPU% computed from delta below
            CPU_PCT=""
        else
            CPU_PCT=0 RSS_KB=0 VSZ_KB=0 THREADS=0
        fi
    fi

    # Network IO (system-wide delta)
    NET_STATS=$(get_net_stats)
    CURR_RX=$(echo "$NET_STATS" | awk '{print $1}')
    CURR_TX=$(echo "$NET_STATS" | awk '{print $2}')
    NET_RX_DELTA=$((CURR_RX - PREV_RX))
    NET_TX_DELTA=$((CURR_TX - PREV_TX))
    PREV_RX="$CURR_RX"
    PREV_TX="$CURR_TX"

    # Go runtime metrics
    METRICS_JSON=$(get_runtime_metrics)
    if [[ -n "$METRICS_JSON" ]]; then
        GOROUTINES=$(echo "$METRICS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('goroutines',''))" 2>/dev/null || echo "")
        HEAP_MB=$(echo "$METRICS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(round(d.get('heap_alloc_bytes',0)/1048576,1))" 2>/dev/null || echo "")
        GC_NUM=$(echo "$METRICS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('gc_num',''))" 2>/dev/null || echo "")
    else
        GOROUTINES="" HEAP_MB="" GC_NUM=""
    fi

    RSS_MB=$(echo "scale=1; $RSS_KB / 1024" | bc 2>/dev/null || echo "0")
    if [[ "$MACOS" -eq 1 ]]; then
        # macOS ps vsz is in bytes, not KB
        VSZ_MB=$(echo "scale=1; $VSZ_KB / 1048576" | bc 2>/dev/null || echo "0")
    else
        VSZ_MB=$(echo "scale=1; $VSZ_KB / 1024" | bc 2>/dev/null || echo "0")
    fi

    ROW="$NOW,$PID,$CPU_PCT,$RSS_MB,$VSZ_MB,$THREADS,$NET_RX_DELTA,$NET_TX_DELTA,$GOROUTINES,$HEAP_MB,$GC_NUM"
    echo "$ROW" >> "$OUTPUT"
    echo "$ROW"

    sleep "$INTERVAL"
done
