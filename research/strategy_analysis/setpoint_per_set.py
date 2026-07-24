"""Per-set analysis of setpoint orders. Answers: which setpoint (by set
number) is actually indicative of a win?

Slices by:
- Set number (1, 2, 3)
- Set × serving (serving vs returning)
- Set × context (home/away set/match point)
- Set × serving × context (triple)

Reads from live PostgreSQL (read-only) via SSH tunnel or direct.
Output: out/setpoint_per_set.json + console.
"""

import json
import os
import statistics
from collections import defaultdict

import psycopg2
import psycopg2.extras

DSN = os.environ.get(
    "DB_DSN",
    "host=127.0.0.1 user=kalshi password=kalshi_dev dbname=kalshi_tennis port=5432 sslmode=disable",
)
OUT_DIR = os.path.join(os.path.dirname(__file__), "out")
os.makedirs(OUT_DIR, exist_ok=True)


def log(msg):
    print(msg, flush=True)


def connect():
    conn = psycopg2.connect(DSN)
    conn.set_session(readonly=True)
    return conn


def load_orders(conn):
    cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
    cur.execute("""
        SELECT o.strategy, o.context, o.match_ticker, o.market_price,
               o.edge_cents, o.resolved_pnl_cents, o.payload, e.series_ticker
        FROM orders o
        LEFT JOIN events e ON e.event_ticker = o.match_ticker
        WHERE o.strategy LIKE 'setpoint%'
          AND o.resolved_pnl_cents IS NOT NULL
        ORDER BY o.ts
    """)
    out = []
    for r in cur.fetchall():
        d = dict(r)
        try:
            d["payload"] = json.loads(d["payload"]) if d["payload"] else {}
        except Exception:
            d["payload"] = {}
        p = d["payload"]
        d["set"] = p.get("set")
        d["is_mp"] = bool(p.get("is_mp", False))
        d["serving"] = bool(p.get("serving", False))
        d["won"] = d["resolved_pnl_cents"] > 0
        d["pnl_d"] = d["resolved_pnl_cents"] / 100.0
        out.append(d)
    return out


def stats(rows, label):
    if not rows:
        log(f"  {label}: (empty)")
        return {}
    n = len(rows)
    wins = sum(1 for r in rows if r["won"])
    win_pct = wins / n
    avg_price = statistics.mean([float(r["market_price"]) for r in rows])
    be = avg_price
    avg_pnl = statistics.mean([r["pnl_d"] for r in rows])
    tot_pnl = sum(r["pnl_d"] for r in rows)
    result = {
        "n": n, "wins": wins, "win_pct": round(win_pct, 4),
        "avg_price": round(avg_price, 4), "break_even": round(be, 4),
        "edge_vs_be": round(win_pct - be, 4),
        "avg_pnl_d": round(avg_pnl, 2), "tot_pnl_d": round(tot_pnl, 2),
    }
    flag = " ***" if result["edge_vs_be"] > 0.05 and n >= 20 else ""
    log(f"  {label:<35} n={n:>4} win={win_pct*100:>5.1f}% avgPx={avg_price:.3f} "
        f"be={be:.3f} edge={result['edge_vs_be']*100:>+5.1f}% "
        f"avgPnL={avg_pnl:>7.2f} totPnL={tot_pnl:>9.2f}{flag}")
    return result


def main():
    conn = connect()
    rows = load_orders(conn)
    log(f"Loaded {len(rows)} setpoint orders with resolved PnL\n")

    results = {}

    log("=== BY SET NUMBER ===")
    for s in (1, 2, 3):
        subset = [r for r in rows if r["set"] == s]
        results[f"set{s}"] = stats(subset, f"Set {s}")

    log("\n=== SET x SERVING ===")
    for s in (1, 2, 3):
        for sv in (True, False):
            subset = [r for r in rows if r["set"] == s and r["serving"] == sv]
            label = f"set{s}|{'serving' if sv else 'returning'}"
            results[label] = stats(subset, label)

    log("\n=== SET x CONTEXT ===")
    for s in (1, 2, 3):
        for ctx in ("home_set_point", "away_set_point", "home_match_point", "away_match_point"):
            subset = [r for r in rows if r["set"] == s and r["context"] == ctx]
            label = f"set{s}|{ctx}"
            results[label] = stats(subset, label)

    log("\n=== SET x SERVING x CONTEXT (triple, n>=10 only) ===")
    for s in (1, 2, 3):
        for sv in (True, False):
            for ctx in ("home_set_point", "away_set_point", "home_match_point", "away_match_point"):
                subset = [r for r in rows if r["set"] == s and r["serving"] == sv and r["context"] == ctx]
                label = f"set{s}|{'srv' if sv else 'ret'}|{ctx}"
                if len(subset) >= 10:
                    results[label] = stats(subset, label)
                else:
                    log(f"  {label:<35} n={len(subset)} (skipped, n<10)")

    log("\n=== SET x MP vs non-MP ===")
    for s in (1, 2, 3):
        for mp in (False, True):
            subset = [r for r in rows if r["set"] == s and r["is_mp"] == mp]
            label = f"set{s}|{'MP' if mp else 'non-MP'}"
            results[label] = stats(subset, label)

    log("\n=== SET x PRICE BUCKET ===")
    def pb(r):
        p = float(r["market_price"])
        if p < 0.20: return "<0.20"
        if p < 0.40: return "0.20-0.40"
        if p < 0.60: return "0.40-0.60"
        if p < 0.80: return "0.60-0.80"
        return "0.80+"
    for s in (1, 2, 3):
        for bucket in ("<0.20", "0.20-0.40", "0.40-0.60", "0.60-0.80", "0.80+"):
            subset = [r for r in rows if r["set"] == s and pb(r) == bucket]
            label = f"set{s}|px={bucket}"
            if len(subset) >= 10:
                results[label] = stats(subset, label)

    log("\n=== SUMMARY: which setpoints win? ===")
    winners = [(k, v) for k, v in results.items()
               if v and v.get("edge_vs_be", 0) > 0.05 and v.get("n", 0) >= 20]
    losers = [(k, v) for k, v in results.items()
              if v and v.get("edge_vs_be", 0) < -0.05 and v.get("n", 0) >= 20]
    log(f"\n  WINNERS (edge > +5%, n >= 20):")
    for k, v in sorted(winners, key=lambda x: x[1]["tot_pnl_d"], reverse=True):
        log(f"    {k:<35} n={v['n']:>4} totPnL={v['tot_pnl_d']:>+9.2f} edge={v['edge_vs_be']*100:>+5.1f}%")
    log(f"\n  LOSERS (edge < -5%, n >= 20):")
    for k, v in sorted(losers, key=lambda x: x[1]["tot_pnl_d"]):
        log(f"    {k:<35} n={v['n']:>4} totPnL={v['tot_pnl_d']:>+9.2f} edge={v['edge_vs_be']*100:>+5.1f}%")

    out_path = os.path.join(OUT_DIR, "setpoint_per_set.json")
    with open(out_path, "w") as f:
        json.dump(results, f, indent=2, default=str)
    log(f"\nWrote {out_path}")


if __name__ == "__main__":
    main()
