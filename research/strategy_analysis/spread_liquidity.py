"""Spread & liquidity patterns: when does the market get wide/thin?

Questions:
  1. How does bid-ask spread evolve over match lifetime? (open → close)
  2. Does spread widen before big moves? (uncertainty → opportunity to fade)
  3. Is spread cheaper at specific score states (break point, set point)?
  4. Volume/trade cadence — when is liquidity thickest?

Wide spread = high cost to enter/exit = strategy must overcome larger edge.
Thin liquidity = slippage risk. Maps where strategies are viable.
"""

import json
import statistics
from collections import defaultdict

from db import connect, log, save_json


def load_markets(conn):
    return [dict(r) for r in conn.execute("""
        SELECT m.market_ticker, m.event_ticker, m.close_ts, m.open_ts, m.result
        FROM markets m
        WHERE m.status='finalized'
          AND m.close_ts IS NOT NULL
          AND (SELECT COUNT(*) FROM ticks t WHERE t.market_ticker=m.market_ticker
               AND t.yes_bid IS NOT NULL AND t.yes_ask IS NOT NULL) >= 100
    """).fetchall()]


def spread_stats(rows):
    spreads = []
    for r in rows:
        if r["yes_bid"] is not None and r["yes_ask"] is not None:
            sp = r["yes_ask"] - r["yes_bid"]
            if 0 <= sp <= 1:
                spreads.append((r["ts"], sp, r["yes_bid_size"], r["yes_ask_size"],
                                r["volume"], r["price"]))
    return spreads


def run():
    conn = connect()
    markets = load_markets(conn)
    log(f"markets with 100+ book ticks: {len(markets)}")

    all_spreads = []
    per_market_summary = []
    for m in markets:
        rows = conn.execute("""
            SELECT ts, price, yes_bid, yes_ask, yes_bid_size, yes_ask_size, volume
            FROM ticks WHERE market_ticker=? AND yes_bid IS NOT NULL
            ORDER BY ts
        """, (m["market_ticker"],)).fetchall()
        sps = spread_stats(rows)
        if not sps:
            continue
        close = m["close_ts"]
        for ts, sp, bs, as_, vol, px in sps:
            # seconds before close (negative = before)
            rel = (ts - close) / 1000.0
            all_spreads.append({
                "market": m["market_ticker"], "rel_s": round(rel, 1),
                "spread": round(sp, 4), "bid_size": bs, "ask_size": as_,
                "volume": vol, "price": px,
            })
        sp_vals = [s[1] for s in sps]
        per_market_summary.append({
            "market": m["market_ticker"],
            "n": len(sp_vals),
            "avg_spread": round(statistics.mean(sp_vals), 4),
            "median_spread": round(statistics.median(sp_vals), 4),
            "max_spread": round(max(sp_vals), 4),
        })

    log(f"total spread samples: {len(all_spreads)}")
    if not all_spreads:
        save_json("spread_liquidity.json", {"summary": {}})
        return

    sp_all = [s["spread"] for s in all_spreads]
    log(f"spread: mean={statistics.mean(sp_all):.4f} "
        f"median={statistics.median(sp_all):.4f} "
        f"p90={sorted(sp_all)[int(len(sp_all)*0.9)]:.4f} "
        f"max={max(sp_all):.4f}")

    # spread by time-to-close bucket
    buckets = defaultdict(list)
    for s in all_spreads:
        # bucket rel_s into 60s bins from -600 to +60
        b = int(s["rel_s"] // 60) * 60
        if -600 <= b <= 0:
            buckets[b].append(s["spread"])
    time_table = []
    log("spread by minutes-before-close:")
    for b in sorted(buckets.keys()):
        v = buckets[b]
        time_table.append({
            "min_before_close": -b // 60,
            "n": len(v),
            "avg_spread_cents": round(statistics.mean(v) * 100, 2),
            "median_cents": round(statistics.median(v) * 100, 2),
        })
    for r in time_table:
        log(f"  {r['min_before_close']:3d}min  n={r['n']:5d} "
            f"avg={r['avg_spread_cents']:5.2f}c med={r['median_cents']:5.2f}c")

    # spread by price level (cheap contracts often wider in % terms)
    price_buckets = defaultdict(list)
    for s in all_spreads:
        if s["price"] is None:
            continue
        if s["price"] < 0.10:
            pb = "<10c"
        elif s["price"] < 0.30:
            pb = "10-30c"
        elif s["price"] < 0.50:
            pb = "30-50c"
        elif s["price"] < 0.70:
            pb = "50-70c"
        elif s["price"] < 0.90:
            pb = "70-90c"
        else:
            pb = "90-100c"
        price_buckets[pb].append(s["spread"])
    price_table = []
    log("spread by price level:")
    for pb in ["<10c", "10-30c", "30-50c", "50-70c", "70-90c", "90-100c"]:
        v = price_buckets.get(pb, [])
        if not v:
            continue
        price_table.append({
            "price_band": pb, "n": len(v),
            "avg_spread_cents": round(statistics.mean(v) * 100, 2),
            "avg_spread_pct_of_price": round(
                statistics.mean(v) / (statistics.mean([s["price"] for s in all_spreads
                    if s["price"] is not None and (
                        (pb == "<10c" and s["price"] < 0.10) or
                        (pb == "10-30c" and 0.10 <= s["price"] < 0.30) or
                        (pb == "30-50c" and 0.30 <= s["price"] < 0.50) or
                        (pb == "50-70c" and 0.50 <= s["price"] < 0.70) or
                        (pb == "70-90c" and 0.70 <= s["price"] < 0.90) or
                        (pb == "90-100c" and s["price"] >= 0.90)
                    )]) or 1) * 100, 2),
        })
    for r in price_table:
        log(f"  {r['price_band']:10s} n={r['n']:5d} avg={r['avg_spread_cents']:5.2f}c "
            f"(% of price: {r['avg_spread_pct_of_price']:.1f}%)")

    # volume cadence: trades per minute by time-to-close
    vol_buckets = defaultdict(int)
    for s in all_spreads:
        if s["volume"] is None:
            continue
        b = int(s["rel_s"] // 60) * 60
        if -600 <= b <= 0:
            vol_buckets[b] += 1
    vol_table = [{"min_before_close": -b // 60, "samples": vol_buckets[b]}
                 for b in sorted(vol_buckets.keys())]

    summary = {
        "n_samples": len(all_spreads),
        "avg_spread": round(statistics.mean(sp_all), 4),
        "median_spread": round(statistics.median(sp_all), 4),
        "spread_by_time_to_close": time_table,
        "spread_by_price_band": price_table,
        "trade_samples_by_time_to_close": vol_table,
        "per_market": per_market_summary[:50],
    }
    save_json("spread_liquidity.json", summary)
    conn.close()
    return summary


if __name__ == "__main__":
    run()
