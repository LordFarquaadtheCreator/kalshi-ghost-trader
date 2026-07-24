#!/usr/bin/env python3
"""Run each setpoint variant across price bands, parse backtest output,
collect summary stats. Outputs table to stdout + TSV file.

Usage: DB_DSN=... python3 scripts/setpoint_bands.py
"""

import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

BIN = os.environ.get("BACKTEST_BIN", "./bin/backtest")
DSN = os.environ.get(
    "DB_DSN",
    "host=127.0.0.1 port=15432 user=kalshi password=kalshi_dev dbname=kalshi_tennis sslmode=disable",
)
OUT_DIR = "research/strategy_analysis/out"
os.makedirs(OUT_DIR, exist_ok=True)
RESULTS_FILE = os.path.join(OUT_DIR, "setpoint_bands.tsv")

STRATEGIES = [
    "setpoint",
    "setpoint-set1",
    "setpoint-set2",
    "setpoint-set2-ret",
]

BANDS = [
    (0.00, 0.20, "<0.20"),
    (0.20, 0.40, "0.20-0.40"),
    (0.40, 0.60, "0.40-0.60"),
    (0.60, 0.80, "0.60-0.80"),
    (0.80, 1.00, "0.80+"),
]


def parse_output(output):
    """Extract summary stats from backtest CLI output."""
    result = {"n": 0, "win_pct": 0, "pnl": 0, "roi": 0, "sharpe": 0, "pf": 0, "avg_price": 0}
    for line in output.splitlines():
        if line.startswith("Total signals:"):
            m = re.search(r"Total signals:\s+(\d+)", line)
            if m:
                result["n"] = int(m.group(1))
        elif line.startswith("Wins:"):
            m = re.search(r"Wins:\s+\d+\s+\(([0-9.]+)%\)", line)
            if m:
                result["win_pct"] = float(m.group(1))
        elif line.startswith("Net P&L:"):
            m = re.search(r"Net P&L:\s+\$(-?[0-9.]+)", line)
            if m:
                result["pnl"] = float(m.group(1))
        elif line.startswith("ROI:"):
            m = re.search(r"ROI:\s+(-?[0-9.]+)%", line)
            if m:
                result["roi"] = float(m.group(1))
        elif "Sharpe (per-trade):" in line:
            m = re.search(r"Sharpe \(per-trade\):\s+(-?[0-9.]+)", line)
            if m:
                result["sharpe"] = float(m.group(1))
        elif "Profit factor:" in line:
            m = re.search(r"Profit factor:\s+([0-9.]+)", line)
            if m:
                result["pf"] = float(m.group(1))
        elif line.startswith("Avg price:"):
            m = re.search(r"Avg price:\s+([0-9.]+)", line)
            if m:
                result["avg_price"] = float(m.group(1))
    return result


def run_one(strat, minp, maxp, band_label):
    cmd = [BIN, "-strategy", strat, "-min-price", str(minp), "-max-price", str(maxp)]
    env = os.environ.copy()
    env["DB_DSN"] = DSN
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, env=env, timeout=120)
        stats = parse_output(proc.stdout)
    except subprocess.TimeoutExpired:
        stats = {"n": 0, "win_pct": 0, "pnl": 0, "roi": 0, "sharpe": 0, "pf": 0, "avg_price": 0}
    except Exception as e:
        print(f"  ERROR {strat}|{band_label}: {e}", file=sys.stderr)
        stats = {"n": 0, "win_pct": 0, "pnl": 0, "roi": 0, "sharpe": 0, "pf": 0, "avg_price": 0}
    return strat, band_label, stats


def main():
    tasks = []
    for strat in STRATEGIES:
        for minp, maxp, label in BANDS:
            tasks.append((strat, minp, maxp, label))

    print(f"Running {len(tasks)} backtests in parallel...")
    results = []
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = {pool.submit(run_one, *t): t for t in tasks}
        for fut in as_completed(futures):
            strat, band, stats = fut.result()
            results.append((strat, band, stats))
            print(f"  {strat:<25} {band:<12} n={stats['n']:>3} win={stats['win_pct']:>5.1f}% "
                  f"pnl=${stats['pnl']:>+8.2f} roi={stats['roi']:>+7.1f}% "
                  f"sharpe={stats['sharpe']:>+7.4f} pf={stats['pf']:>5.2f}")

    # Sort by strategy then band order
    band_order = {b[2]: i for i, b in enumerate(BANDS)}
    strat_order = {s: i for i, s in enumerate(STRATEGIES)}
    results.sort(key=lambda x: (strat_order.get(x[0], 99), band_order.get(x[1], 99)))

    # Write TSV
    with open(RESULTS_FILE, "w") as f:
        f.write("strategy\tband\tn\twin_pct\tpnl_d\troi_pct\tsharpe\tpf\tavg_price\n")
        for strat, band, s in results:
            f.write(f"{strat}\t{band}\t{s['n']}\t{s['win_pct']}\t{s['pnl']}\t{s['roi']}\t{s['sharpe']}\t{s['pf']}\t{s['avg_price']}\n")

    # Print formatted table
    print(f"\n{'='*120}")
    print(f"{'strategy':<25} {'band':<12} {'n':>4} {'win%':>7} {'pnl$':>10} {'roi%':>8} {'sharpe':>8} {'pf':>6} {'avgPx':>7}")
    print(f"{'-'*120}")
    for strat, band, s in results:
        flag = " ***" if s["pnl"] > 0 and s["n"] >= 5 else ""
        print(f"{strat:<25} {band:<12} {s['n']:>4} {s['win_pct']:>6.1f}% {s['pnl']:>+10.2f} "
              f"{s['roi']:>+7.1f}% {s['sharpe']:>+8.4f} {s['pf']:>5.2f} {s['avg_price']:>7.3f}{flag}")

    # Best per band
    print(f"\n{'='*60}")
    print("BEST STRATEGY PER BAND (by PnL, n>=5):")
    print(f"{'-'*60}")
    for minp, maxp, label in BANDS:
        band_results = [(strat, s) for strat, band, s in results if band == label and s["n"] >= 5]
        if band_results:
            best = max(band_results, key=lambda x: x[1]["pnl"])
            print(f"  {label:<12} -> {best[0]:<25} n={best[1]['n']:>3} pnl=${best[1]['pnl']:>+8.2f} "
                  f"sharpe={best[1]['sharpe']:>+7.4f} pf={best[1]['pf']:>5.2f}")
        else:
            print(f"  {label:<12} -> (no strategy with n>=5)")

    print(f"\nResults written to {RESULTS_FILE}")


if __name__ == "__main__":
    main()
