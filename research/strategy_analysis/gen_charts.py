#!/usr/bin/env python3
"""Generate backtest comparison charts for all strategies.

Reads backtest results from inline runs and produces charts in
research/charts/backtest/.
"""

import os
import sys
import json
import subprocess
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as mticker
import numpy as np

CHART_DIR = os.path.join(os.path.dirname(__file__), "..", "charts", "backtest")
os.makedirs(CHART_DIR, exist_ok=True)

STRATEGIES = [
    "matchpoint",
    "matchpoint-aggro",
    "setpoint",
    "setpoint-serve",
    "setpoint-cheap",
    "fadelongshot",
]

DB_PATH = os.path.join(os.path.dirname(__file__), "..", "..", "kalshi_tennis.db")


def run_backtest(strategy: str) -> dict:
    """Run backtest for a strategy and parse output."""
    cmd = ["go", "run", "./cmd/backtest", "-strategy", strategy, "-db", DB_PATH]
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=os.path.join(os.path.dirname(__file__), "..", ".."))
    output = result.stdout + result.stderr

    # Parse summary lines
    data = {"strategy": strategy, "orders": [], "raw": output}
    for line in output.splitlines():
        line = line.strip()
        if line.startswith("Total signals:"):
            data["signals"] = int(line.split(":")[1].strip())
        elif line.startswith("Wins:"):
            parts = line.split()
            data["wins"] = int(parts[1])
            data["win_pct"] = float(parts[2].strip("()%"))
        elif line.startswith("Losses:"):
            parts = line.split()
            data["losses"] = int(parts[1])
        elif line.startswith("Total invested:"):
            data["invested"] = float(line.split("$")[1])
        elif line.startswith("Total payout:"):
            data["payout"] = float(line.split("$")[1])
        elif line.startswith("Net P&L:"):
            data["pnl"] = float(line.split("$")[1])
        elif line.startswith("ROI:"):
            data["roi"] = float(line.split(":")[1].strip("%"))
        elif line.startswith("Avg edge:"):
            data["avg_edge"] = float(line.split(":")[1].split()[0])
        elif line.startswith("Avg size:"):
            data["avg_size"] = float(line.split(":")[1].split()[0])
        elif line.startswith("Avg price:"):
            data["avg_price"] = float(line.split(":")[1].split()[0])

    # Parse per-order lines (after the header dashes)
    in_orders = False
    for line in output.splitlines():
        if line.startswith("-" * 20):
            in_orders = True
            continue
        if in_orders and line.strip() and not line.startswith("="):
            parts = line.split()
            if len(parts) >= 7:
                try:
                    data["orders"].append({
                        "match": parts[0],
                        "context": parts[1] if len(parts) > 1 else "",
                        "price": float(parts[-5]) if len(parts) >= 6 else 0,
                        "won": parts[-2] == "Y" if len(parts) >= 2 else False,
                        "pnl": float(parts[-1]) if len(parts) >= 1 else 0,
                    })
                except (ValueError, IndexError):
                    pass

    return data


def chart_pnl_comparison(results: list[dict]):
    """Bar chart: Net P&L per strategy."""
    fig, ax = plt.subplots(figsize=(10, 6))
    names = [r["strategy"] for r in results]
    pnls = [r.get("pnl", 0) for r in results]
    colors = ["#2ecc71" if p > 0 else "#e74c3c" for p in pnls]
    bars = ax.bar(names, pnls, color=colors, edgecolor="black", linewidth=0.5)
    ax.axhline(y=0, color="black", linewidth=0.8)
    ax.set_ylabel("Net P&L ($)")
    ax.set_title("Net P&L by Strategy")
    ax.set_xlabel("Strategy")
    plt.xticks(rotation=30, ha="right")
    for bar, val in zip(bars, pnls):
        ax.text(bar.get_x() + bar.get_width() / 2., bar.get_height(),
                f"${val:.0f}", ha="center", va="bottom" if val > 0 else "top",
                fontsize=9, fontweight="bold")
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "pnl_comparison.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def chart_roi_comparison(results: list[dict]):
    """Bar chart: ROI per strategy."""
    fig, ax = plt.subplots(figsize=(10, 6))
    names = [r["strategy"] for r in results]
    rois = [r.get("roi", 0) for r in results]
    colors = ["#2ecc71" if r > 0 else "#e74c3c" for r in rois]
    bars = ax.bar(names, rois, color=colors, edgecolor="black", linewidth=0.5)
    ax.axhline(y=0, color="black", linewidth=0.8)
    ax.set_ylabel("ROI (%)")
    ax.set_title("ROI by Strategy")
    ax.set_xlabel("Strategy")
    plt.xticks(rotation=30, ha="right")
    for bar, val in zip(bars, rois):
        ax.text(bar.get_x() + bar.get_width() / 2., bar.get_height(),
                f"{val:.1f}%", ha="center", va="bottom" if val > 0 else "top",
                fontsize=9, fontweight="bold")
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "roi_comparison.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def chart_winrate(results: list[dict]):
    """Bar chart: Win rate per strategy."""
    fig, ax = plt.subplots(figsize=(10, 6))
    names = [r["strategy"] for r in results]
    win_rates = [r.get("win_pct", 0) for r in results]
    signals = [r.get("signals", 0) for r in results]
    colors = plt.cm.RdYlGn([w / 100 for w in win_rates])
    bars = ax.bar(names, win_rates, color=colors, edgecolor="black", linewidth=0.5)
    ax.axhline(y=50, color="gray", linewidth=0.8, linestyle="--", label="50% breakeven")
    ax.set_ylabel("Win Rate (%)")
    ax.set_title("Win Rate by Strategy")
    ax.set_xlabel("Strategy")
    ax.set_ylim(0, 110)
    ax.legend()
    plt.xticks(rotation=30, ha="right")
    for bar, wr, n in zip(bars, win_rates, signals):
        ax.text(bar.get_x() + bar.get_width() / 2., bar.get_height() + 2,
                f"{wr:.1f}%\n(n={n})", ha="center", va="bottom", fontsize=8)
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "winrate_comparison.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def chart_signal_volume(results: list[dict]):
    """Bar chart: Number of signals per strategy."""
    fig, ax = plt.subplots(figsize=(10, 6))
    names = [r["strategy"] for r in results]
    signals = [r.get("signals", 0) for r in results]
    bars = ax.bar(names, signals, color="#3498db", edgecolor="black", linewidth=0.5)
    ax.set_ylabel("Number of Signals")
    ax.set_title("Signal Volume by Strategy")
    ax.set_xlabel("Strategy")
    plt.xticks(rotation=30, ha="right")
    for bar, val in zip(bars, signals):
        ax.text(bar.get_x() + bar.get_width() / 2., bar.get_height() + 1,
                str(val), ha="center", va="bottom", fontsize=9, fontweight="bold")
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "signal_volume.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def chart_edge_vs_price(results: list[dict]):
    """Scatter: avg edge vs avg price, bubble size = signal count."""
    fig, ax = plt.subplots(figsize=(10, 7))
    for r in results:
        edge = r.get("avg_edge", 0)
        price = r.get("avg_price", 0)
        n = r.get("signals", 1)
        pnl = r.get("pnl", 0)
        color = "#2ecc71" if pnl > 0 else "#e74c3c"
        ax.scatter(price, edge, s=max(n * 3, 50), c=color, alpha=0.7,
                   edgecolors="black", linewidth=0.5)
        ax.annotate(f"{r['strategy']}\n(n={n})", (price, edge),
                    textcoords="offset points", xytext=(10, 5), fontsize=8)
    ax.set_xlabel("Avg Market Price (0-1)")
    ax.set_ylabel("Avg Edge (cents)")
    ax.set_title("Edge vs Price by Strategy (bubble size = signal count)")
    ax.axhline(y=0, color="gray", linewidth=0.5)
    ax.grid(True, alpha=0.3)
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "edge_vs_price.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def chart_pnl_per_order(results: list[dict]):
    """Box plot: P&L distribution per order for each strategy."""
    fig, ax = plt.subplots(figsize=(12, 6))
    data = []
    labels = []
    for r in results:
        pnls = [o["pnl"] for o in r.get("orders", []) if "pnl" in o]
        if pnls:
            data.append(pnls)
            labels.append(r["strategy"])
    if data:
        bp = ax.boxplot(data, tick_labels=labels, patch_artist=True, showmeans=True)
        for patch, r in zip(bp["boxes"], results):
            pnl = r.get("pnl", 0)
            patch.set_facecolor("#2ecc71" if pnl > 0 else "#e74c3c")
            patch.set_alpha(0.6)
    ax.axhline(y=0, color="black", linewidth=0.8)
    ax.set_ylabel("P&L per Order ($)")
    ax.set_title("P&L Distribution per Order by Strategy")
    ax.set_xlabel("Strategy")
    plt.xticks(rotation=30, ha="right")
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "pnl_distribution.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def chart_summary_table(results: list[dict]):
    """Summary table as an image."""
    fig, ax = plt.subplots(figsize=(14, 5))
    ax.axis("off")
    cols = ["Strategy", "Signals", "Win%", "Net P&L", "ROI%", "Avg Edge", "Avg Price", "Invested"]
    rows = []
    for r in results:
        rows.append([
            r["strategy"],
            str(r.get("signals", 0)),
            f"{r.get('win_pct', 0):.1f}%",
            f"${r.get('pnl', 0):.0f}",
            f"{r.get('roi', 0):.1f}%",
            f"{r.get('avg_edge', 0):.1f}c",
            f"{r.get('avg_price', 0):.3f}",
            f"${r.get('invested', 0):.0f}",
        ])
    table = ax.table(cellText=rows, colLabels=cols, loc="center", cellLoc="center")
    table.auto_set_font_size(False)
    table.set_fontsize(10)
    table.scale(1.2, 1.8)
    # Color header
    for j in range(len(cols)):
        table[0, j].set_facecolor("#2c3e50")
        table[0, j].set_text_props(color="white", fontweight="bold")
    # Color P&L cells
    for i, r in enumerate(results):
        pnl = r.get("pnl", 0)
        color = "#d5f5e3" if pnl > 0 else "#fadbd8"
        table[i + 1, 3].set_facecolor(color)
    ax.set_title("Backtest Results Summary", fontsize=14, fontweight="bold", pad=20)
    plt.tight_layout()
    path = os.path.join(CHART_DIR, "summary_table.png")
    plt.savefig(path, dpi=150)
    plt.close()
    print(f"Saved {path}")


def main():
    results = []
    for strat in STRATEGIES:
        print(f"Running backtest: {strat}...")
        data = run_backtest(strat)
        results.append(data)
        print(f"  signals={data.get('signals', 0)}, pnl=${data.get('pnl', 0):.0f}, roi={data.get('roi', 0):.1f}%")

    # Save raw results as JSON
    json_path = os.path.join(CHART_DIR, "backtest_results.json")
    with open(json_path, "w") as f:
        json.dump(results, f, indent=2, default=str)
    print(f"Saved {json_path}")

    # Generate all charts
    chart_pnl_comparison(results)
    chart_roi_comparison(results)
    chart_winrate(results)
    chart_signal_volume(results)
    chart_edge_vs_price(results)
    chart_pnl_per_order(results)
    chart_summary_table(results)

    print(f"\nAll charts saved to {CHART_DIR}")


if __name__ == "__main__":
    main()
