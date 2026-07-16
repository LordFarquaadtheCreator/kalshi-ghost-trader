"""Run all strategy-analysis modules in sequence, write a combined summary.

Each module writes its own out/<name>.json. This collects them and prints
a single ranked summary of tradeable strategies.
"""

import json
from pathlib import Path

import overview
import nothing_happens
import match_point_edge
import spike_reversion
import orderbook_imbalance
import spread_liquidity

from db import log, OUT_DIR


def run_all():
    log("\n=== OVERVIEW ===")
    overview.run()
    log("\n=== NOTHING HAPPENS (fade longshot) ===")
    nothing_happens.run()
    log("\n=== MATCH POINT EDGE ===")
    match_point_edge.run()
    log("\n=== SPIKE REVERSION ===")
    spike_reversion.run()
    log("\n=== ORDERBOOK IMBALANCE ===")
    orderbook_imbalance.run()
    log("\n=== SPREAD & LIQUIDITY ===")
    spread_liquidity.run()
    log("\n=== DONE ===")


def load_results():
    out = {}
    for name in ["overview", "nothing_happens", "match_point_edge",
                 "spike_reversion", "orderbook_imbalance", "spread_liquidity"]:
        p = OUT_DIR / f"{name}.json"
        if p.exists():
            out[name] = json.loads(p.read_text())
    return out


if __name__ == "__main__":
    run_all()
