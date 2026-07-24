"""Slice setpoint orders by every dimension available in payload + match
meta to find which setpoint contexts are actually predictive of a win.

Reads from live PostgreSQL (read-only). Prints a table per dimension
sorted by total P&L descending. Flags slices where win-rate clears the
break-even implied by avg entry price.

Output: out/setpoint_slices.json + console.
"""

import json
import os
import statistics
from collections import defaultdict
from datetime import datetime, timezone

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
    """All setpoint-family orders with resolved PnL + match series."""
    cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
    cur.execute(
        """
        SELECT o.id, o.strategy, o.context, o.match_ticker, o.market_ticker,
               o.action, o.conv_prob, o.market_price, o.edge_cents,
               o.suggested_size, o.set_number, o.result, o.resolved_pnl_cents,
               o.settled_ts, o.ts, o.payload, e.series_ticker
        FROM orders o
        LEFT JOIN events e ON e.event_ticker = o.match_ticker
        WHERE o.strategy LIKE 'setpoint%'
          AND o.resolved_pnl_cents IS NOT NULL
        ORDER BY o.ts
        """
    )
    out = []
    for r in cur.fetchall():
        d = dict(r)
        try:
            d["payload"] = json.loads(d["payload"]) if d["payload"] else {}
        except Exception:
            d["payload"] = {}
        # normalize: scorer==winner-of-point, but we bet on the player who
        # *can* win the set, which is the strategy's "winner" field.
        # payload has: home_games, away_games, home_points, away_points,
        # server, scorer, set, serving, is_mp, set_score (sets_home-sets_away
        # before this set), game, is_tiebreak (sometimes missing on old rows).
        p = d["payload"]
        d["set"] = p.get("set", d.get("set_number"))
        d["is_mp"] = bool(p.get("is_mp", False))
        d["is_tb"] = bool(p.get("is_tiebreak", False))
        d["serving"] = bool(p.get("serving", False))
        d["set_score"] = p.get("set_score", "")
        d["home_games"] = p.get("home_games")
        d["away_games"] = p.get("away_games")
        d["home_points"] = p.get("home_points")
        d["away_points"] = p.get("away_points")
        d["server"] = p.get("server")
        d["scorer"] = p.get("scorer")
        d["won"] = d["resolved_pnl_cents"] > 0
        d["pnl_d"] = d["resolved_pnl_cents"] / 100.0
        out.append(d)
    return out


def slice_stats(rows, key_fn, label):
    """Group rows by key_fn, compute stats per group."""
    groups = defaultdict(list)
    for r in rows:
        k = key_fn(r)
        if k is None:
            continue
        groups[k].append(r)

    out = []
    for k, grp in groups.items():
        n = len(grp)
        wins = sum(1 for r in grp if r["won"])
        win_pct = wins / n if n else 0
        avg_price = statistics.mean([float(r["market_price"]) for r in grp])
        # break-even win rate for a buy at avg_price: payoff = (1 - price)/price
        # so win_rate needed to break even = price (since you pay `price` to
        # win 1, EV = win_rate * 1 - price).
        be = avg_price
        avg_pnl = statistics.mean([r["pnl_d"] for r in grp])
        tot_pnl = sum(r["pnl_d"] for r in grp)
        avg_edge = statistics.mean([r["edge_cents"] for r in grp if r["edge_cents"] is not None])
        out.append({
            "key": str(k),
            "n": n,
            "wins": wins,
            "win_pct": round(win_pct, 4),
            "avg_price": round(avg_price, 4),
            "break_even": round(be, 4),
            "edge_vs_be": round(win_pct - be, 4),
            "avg_pnl_d": round(avg_pnl, 2),
            "tot_pnl_d": round(tot_pnl, 2),
            "avg_edge_cents": round(avg_edge, 2),
        })
    out.sort(key=lambda x: x["tot_pnl_d"], reverse=True)
    log(f"\n=== {label} ===")
    log(f"{'key':<28} {'n':>5} {'win%':>6} {'avgPx':>7} {'be':>6} {'edge':>7} {'avgPnL':>8} {'totPnL':>9}")
    for r in out:
        flag = " *" if r["edge_vs_be"] > 0.05 and r["n"] >= 10 else ""
        log(f"{r['key']:<28} {r['n']:>5} {r['win_pct']*100:>5.1f}% {r['avg_price']:>7.3f} "
            f"{r['break_even']:>6.3f} {r['edge_vs_be']*100:>+6.1f}% {r['avg_pnl_d']:>8.2f} "
            f"{r['tot_pnl_d']:>9.2f}{flag}")
    return out


def main():
    conn = connect()
    rows = load_orders(conn)
    log(f"Loaded {len(rows)} setpoint orders with resolved PnL")

    results = {}

    # 1. By strategy
    results["strategy"] = slice_stats(rows, lambda r: r["strategy"], "By strategy")

    # 2. By context (home/away × set/match × tb)
    results["context"] = slice_stats(rows, lambda r: r["context"], "By context")

    # 3. By set number
    results["set_number"] = slice_stats(rows, lambda r: r["set"], "By set number")

    # 4. By serving vs returning
    results["serving"] = slice_stats(
        rows, lambda r: "serving" if r["serving"] else "returning", "Serving vs returning"
    )

    # 5. By tiebreak
    results["tiebreak"] = slice_stats(rows, lambda r: "tb" if r["is_tb"] else "regular", "Tiebreak vs regular")

    # 6. By set_score (sets won before this set)
    results["set_score"] = slice_stats(rows, lambda r: r["set_score"] or "?", "Set score (sets won before)")

    # 7. By series
    results["series"] = slice_stats(rows, lambda r: r["series_ticker"] or "?", "By series")

    # 8. By market price bucket
    def price_bucket(r):
        p = float(r["market_price"])
        if p < 0.20:
            return "<0.20"
        if p < 0.40:
            return "0.20-0.40"
        if p < 0.60:
            return "0.40-0.60"
        if p < 0.80:
            return "0.60-0.80"
        return "0.80+"
    results["price_bucket"] = slice_stats(rows, price_bucket, "By market price bucket")

    # 9. By edge bucket
    def edge_bucket(r):
        e = r["edge_cents"]
        if e is None:
            return None
        if e < 5:
            return "<5c"
        if e < 10:
            return "5-10c"
        if e < 20:
            return "10-20c"
        return "20c+"
    results["edge_bucket"] = slice_stats(rows, edge_bucket, "By edge bucket")

    # 10. By game score (e.g. 5-4, 6-5, 5-3)
    def game_score(r):
        hg, ag = r["home_games"], r["away_games"]
        if hg is None or ag is None:
            return None
        return f"{hg}-{ag}"
    results["game_score"] = slice_stats(rows, game_score, "By game score")

    # 11. By point score (40-30, A-40, etc)
    results["point_score"] = slice_stats(
        rows, lambda r: f"{r['home_points']}-{r['away_points']}" if r["home_points"] else None,
        "By point score",
    )

    # 12. Combined: context × serving
    results["context_serving"] = slice_stats(
        rows,
        lambda r: f"{r['context']}|{'srv' if r['serving'] else 'ret'}",
        "Context × serving",
    )

    # 13. Combined: set × serving
    results["set_serving"] = slice_stats(
        rows,
        lambda r: f"set{r['set']}|{'srv' if r['serving'] else 'ret'}",
        "Set × serving",
    )

    # 14. Combined: set_score × serving
    results["setscore_serving"] = slice_stats(
        rows,
        lambda r: f"{r['set_score'] or '?'}|{'srv' if r['serving'] else 'ret'}",
        "Set score × serving",
    )

    # 15. Combined: context × set_score
    results["context_setscore"] = slice_stats(
        rows,
        lambda r: f"{r['context']}|{r['set_score'] or '?'}",
        "Context × set score",
    )

    # 16. Combined: set × set_score (which set + sets won before)
    results["set_setscore"] = slice_stats(
        rows,
        lambda r: f"set{r['set']}|{r['set_score'] or '?'}",
        "Set × set score",
    )

    # 17. Combined: context × series-tier (ATP/WTA main vs ITF vs Challenger)
    def tier(r):
        s = r["series_ticker"] or ""
        if "CHALLENGER" in s:
            return "challenger"
        if s.startswith("KXITF"):
            return "itf"
        if s in ("KXATPMATCH", "KXWTAMATCH"):
            return "main"
        return "other"
    results["context_tier"] = slice_stats(
        rows, lambda r: f"{r['context']}|{tier(r)}", "Context × tier"
    )

    # 18. Match point only — sliced by set_score (the actual match-point scenario)
    mp_rows = [r for r in rows if r["is_mp"]]
    log(f"\n=== Match-point only (n={len(mp_rows)}) ===")
    results["mp_set_score"] = slice_stats(mp_rows, lambda r: r["set_score"] or "?", "MP by set score")
    results["mp_context"] = slice_stats(mp_rows, lambda r: r["context"], "MP by context")
    results["mp_serving"] = slice_stats(
        mp_rows, lambda r: "serving" if r["serving"] else "returning", "MP serving vs returning"
    )

    # 19. Non-match-point set points — sliced
    sp_rows = [r for r in rows if not r["is_mp"]]
    log(f"\n=== Non-MP set points only (n={len(sp_rows)}) ===")
    results["sp_set_score"] = slice_stats(sp_rows, lambda r: r["set_score"] or "?", "SP by set score")
    results["sp_context"] = slice_stats(sp_rows, lambda r: r["context"], "SP by context")
    results["sp_serving"] = slice_stats(
        sp_rows, lambda r: "serving" if r["serving"] else "returning", "SP serving vs returning"
    )
    results["sp_set_setscore"] = slice_stats(
        sp_rows, lambda r: f"set{r['set']}|{r['set_score'] or '?'}", "SP set × set score"
    )

    # 20. Conversion prob bucket (Markov fair value)
    def conv_bucket(r):
        c = float(r["conv_prob"]) if r["conv_prob"] is not None else None
        if c is None:
            return None
        if c < 0.40:
            return "<0.40"
        if c < 0.60:
            return "0.40-0.60"
        if c < 0.80:
            return "0.60-0.80"
        return "0.80+"
    results["conv_bucket"] = slice_stats(rows, conv_bucket, "By conv_prob bucket")

    out_path = os.path.join(OUT_DIR, "setpoint_slices.json")
    with open(out_path, "w") as f:
        json.dump(results, f, indent=2, default=str)
    log(f"\nWrote {out_path}")


if __name__ == "__main__":
    main()
