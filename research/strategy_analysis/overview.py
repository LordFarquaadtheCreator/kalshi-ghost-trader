"""Dataset overview: counts, time ranges, settled-match quality, gold set.

Establishes the universe of tradeable evidence before any strategy work.
"""

import json
from datetime import datetime, timezone

from db import connect, log, save_json


def fmt_ts(ms):
    if not ms:
        return None
    return datetime.fromtimestamp(ms / 1000, tz=timezone.utc).isoformat()


def run():
    conn = connect()
    c = conn.cursor()

    def one(q, *args):
        return c.execute(q, args).fetchone()

    counts = {}
    for tbl in [
        "events", "markets", "ticks", "orderbook_events",
        "lifecycle_events", "event_lifecycle_events", "points",
        "flashscore_matches", "orders", "scan_runs",
    ]:
        counts[tbl] = c.execute(f"SELECT COUNT(*) FROM {tbl}").fetchone()[0]

    # markets by status
    status = dict(c.execute(
        "SELECT status, COUNT(*) FROM markets GROUP BY status").fetchall())
    # events by series
    series = c.execute(
        "SELECT series_ticker, COUNT(*) FROM events "
        "GROUP BY series_ticker ORDER BY 2 DESC").fetchall()
    # finalized with both yes+no result
    finalized_events = c.execute("""
        SELECT COUNT(DISTINCT e.event_ticker)
        FROM events e
        WHERE EXISTS (SELECT 1 FROM markets m
                      WHERE m.event_ticker=e.event_ticker
                      AND m.status='finalized' AND m.result='yes')
          AND EXISTS (SELECT 1 FROM markets m
                      WHERE m.event_ticker=e.event_ticker
                      AND m.status='finalized' AND m.result='no')
    """).fetchone()[0]

    # gold set: settled + points + ticks overlap
    gold = c.execute("""
        SELECT COUNT(DISTINCT p.match_ticker)
        FROM points p
        WHERE p.ts_ms IS NOT NULL
          AND EXISTS (SELECT 1 FROM markets m, ticks t
                      WHERE m.event_ticker=p.match_ticker
                        AND t.market_ticker=m.market_ticker
                        AND m.status='finalized')
    """).fetchone()[0]

    # tick/point time range
    tr = c.execute("SELECT MIN(ts), MAX(ts) FROM ticks").fetchone()
    pr = c.execute(
        "SELECT MIN(ts_ms), MAX(ts_ms) FROM points WHERE ts_ms IS NOT NULL"
    ).fetchone()

    # ticks with price / by msg_type
    ticks_price = c.execute(
        "SELECT COUNT(*) FROM ticks WHERE price IS NOT NULL").fetchone()[0]
    msg_types = dict(c.execute(
        "SELECT msg_type, COUNT(*) FROM ticks GROUP BY msg_type").fetchall())

    # points per match distribution (gold set)
    pts_per_match = [r[0] for r in c.execute("""
        SELECT COUNT(*) FROM points p
        WHERE p.ts_ms IS NOT NULL
        GROUP BY p.match_ticker
    """).fetchall()]

    # ticks per market distribution (settled markets only)
    ticks_per_market = [r[0] for r in c.execute("""
        SELECT COUNT(*) FROM ticks t
        JOIN markets m ON t.market_ticker=m.market_ticker
        WHERE m.status='finalized'
        GROUP BY t.market_ticker
    """).fetchall()]

    def stats(xs):
        if not xs:
            return None
        xs = sorted(xs)
        n = len(xs)
        return {
            "n": n,
            "min": xs[0],
            "p25": xs[n // 4],
            "median": xs[n // 2],
            "p75": xs[3 * n // 4],
            "p90": xs[int(n * 0.9)],
            "max": xs[-1],
            "mean": round(sum(xs) / n, 2),
        }

    out = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "counts": counts,
        "markets_by_status": status,
        "events_by_series": dict(series),
        "finalized_events_with_both_markets": finalized_events,
        "gold_set_settled_with_points_and_ticks": gold,
        "tick_time_range": [fmt_ts(tr[0]), fmt_ts(tr[1])],
        "point_time_range": [fmt_ts(pr[0]), fmt_ts(pr[1])],
        "ticks_with_price": ticks_price,
        "ticks_by_msg_type": msg_types,
        "points_per_match": stats(pts_per_match),
        "ticks_per_settled_market": stats(ticks_per_market),
    }

    save_json("overview.json", out)

    log("=== DATASET OVERVIEW ===")
    log(f"counts: {json.dumps(counts, indent=2)}")
    log(f"markets by status: {status}")
    log(f"finalized events (both mkts): {finalized_events}")
    log(f"gold set (settled+pts+ticks): {gold}")
    log(f"tick range: {out['tick_time_range']}")
    log(f"point range: {out['point_time_range']}")
    log(f"ticks by msg_type: {msg_types}")
    log(f"points/match: {out['points_per_match']}")
    log(f"ticks/settled market: {out['ticks_per_settled_market']}")
    conn.close()
    return out


if __name__ == "__main__":
    run()
