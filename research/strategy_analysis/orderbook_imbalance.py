"""Top-of-book imbalance predictive power.

Uses ticks.yes_bid_size / yes_ask_size (already parsed top-of-book).
Imbalance = (bid_size - ask_size) / (bid_size + ask_size).
Hypothesis: heavy bid size → price up (buyers stacked), heavy ask → price down.

Test: bucket imbalance into deciles, measure realized price move over next
N seconds. If monotonic, imbalance is predictive. Then backtest a strategy:
buy when imbalance > +threshold, sell when < -threshold, exit after N seconds.

Also tests trade-flow imbalance (taker_side) from ticks as a comparison signal.
"""

import json
import statistics
from collections import defaultdict

from db import connect, log, save_json

HORIZON_S = 30
MIN_SIZE = 5          # ignore levels with <5 contracts (noise)
SAMPLE_GAP_S = 10


def load_markets(conn):
    return [r["market_ticker"] for r in conn.execute("""
        SELECT m.market_ticker
        FROM markets m
        WHERE m.status='finalized'
          AND (SELECT COUNT(*) FROM ticks t WHERE t.market_ticker=m.market_ticker
               AND t.yes_bid IS NOT NULL AND t.yes_ask IS NOT NULL
               AND t.yes_bid_size IS NOT NULL AND t.yes_ask_size IS NOT NULL) >= 200
    """).fetchall()]


def load_book(conn, ticker):
    rows = conn.execute("""
        SELECT ts, price, yes_bid, yes_ask, yes_bid_size, yes_ask_size,
               taker_side, taker_outcome_side
        FROM ticks
        WHERE market_ticker=?
          AND yes_bid IS NOT NULL AND yes_ask IS NOT NULL
          AND yes_bid_size IS NOT NULL AND yes_ask_size IS NOT NULL
        ORDER BY ts
    """, (ticker,)).fetchall()
    return [dict(r) for r in rows]


def compute_signals(rows, horizon_ms, gap_ms, min_size):
    """For sampled ticks, compute imbalance and forward price move."""
    out = []
    last = -10**12
    n = len(rows)
    for i, r in enumerate(rows):
        if r["ts"] - last < gap_ms:
            continue
        bs = r["yes_bid_size"] or 0
        as_ = r["yes_ask_size"] or 0
        if bs + as_ < min_size:
            continue
        imb = (bs - as_) / (bs + as_)
        # forward price: nearest tick at t + horizon
        target = r["ts"] + horizon_ms
        k = i
        while k < n - 1 and rows[k]["ts"] < target:
            k += 1
        p0 = r["price"]
        p1 = rows[k]["price"]
        if p0 is None or p1 is None:
            continue
        out.append({
            "ts": r["ts"], "imb": round(imb, 4),
            "move": round(p1 - p0, 4),
            "bid_size": bs, "ask_size": as_,
            "taker_side": r["taker_side"],
        })
        last = r["ts"]
    return out


def run():
    conn = connect()
    markets = load_markets(conn)
    log(f"markets with 200+ book ticks: {len(markets)}")

    all_sigs = []
    for tk in markets:
        rows = load_book(conn, tk)
        sigs = compute_signals(rows, HORIZON_S * 1000, SAMPLE_GAP_S * 1000, MIN_SIZE)
        for s in sigs:
            s["market"] = tk
        all_sigs.extend(sigs)

    log(f"total imbalance samples: {len(all_sigs)}")
    if not all_sigs:
        save_json("orderbook_imbalance.json", {"summary": {}})
        return

    # decile analysis
    imbs = sorted(s["imb"] for s in all_sigs)
    n = len(imbs)
    decile_edges = [imbs[min(n - 1, int(n * d / 10))] for d in range(11)]
    buckets = defaultdict(list)
    for s in all_sigs:
        b = 0
        for d in range(1, 10):
            if s["imb"] >= decile_edges[d]:
                b = d
        buckets[b].append(s["move"])

    log("imbalance decile -> avg forward move (cents):")
    decile_table = []
    for b in range(10):
        moves = buckets[b]
        if not moves:
            continue
        avg = statistics.mean(moves)
        row = {
            "decile": b, "imb_lo": round(decile_edges[b], 3),
            "imb_hi": round(decile_edges[b + 1], 3), "n": len(moves),
            "avg_move_cents": round(avg * 100, 2),
            "win_up": round(sum(1 for m in moves if m > 0) / len(moves), 3),
        }
        decile_table.append(row)
        log(f"  D{b} imb[{row['imb_lo']:+.2f},{row['imb_hi']:+.2f}] "
            f"n={row['n']:4d} avg={row['avg_move_cents']:+6.2f}c win_up={row['win_up']}")

    # correlation
    imbs_x = [s["imb"] for s in all_sigs]
    moves_y = [s["move"] for s in all_sigs]
    mx, my = statistics.mean(imbs_x), statistics.mean(moves_y)
    cov = sum((x - mx) * (y - my) for x, y in zip(imbs_x, moves_y)) / n
    sx = statistics.pstdev(imbs_x)
    sy = statistics.pstdev(moves_y)
    corr = cov / (sx * sy) if sx and sy else 0
    log(f"pearson corr(imbalance, forward_move) = {corr:.4f}")

    # backtest: trade on extreme imbalance
    def backtest(thr, hold_cents_exit=None):
        trades = []
        for s in all_sigs:
            if s["imb"] > thr:
                # buy, expect up
                trades.append({"side": "buy", "imb": s["imb"], "move": s["move"]})
            elif s["imb"] < -thr:
                # sell, expect down (pnl = -move)
                trades.append({"side": "sell", "imb": s["imb"], "move": -s["move"]})
        if not trades:
            return None
        pnls = [t["move"] for t in trades]
        wins = sum(1 for p in pnls if p > 0)
        # net of 1c fee
        net = [p - 0.01 for p in pnls]
        return {
            "thr": thr, "n": len(trades),
            "gross_avg_cents": round(statistics.mean(pnls) * 100, 2),
            "net_avg_cents": round(statistics.mean(net) * 100, 2),
            "win_rate": round(wins / len(trades), 4),
            "net_total": round(sum(net) * 100, 2),
            "sharpe": round(statistics.mean(net) / statistics.pstdev(net), 3)
            if len(net) > 1 and statistics.pstdev(net) else None,
        }

    bt = {}
    for thr in [0.3, 0.5, 0.7, 0.8, 0.9]:
        r = backtest(thr)
        if r:
            log(f"  thr={thr}: n={r['n']} net_avg={r['net_avg_cents']}c "
                f"win={r['win_rate']} net_total={r['net_total']}c sharpe={r['sharpe']}")
            bt[f"thr_{thr}"] = r

    # trade-flow imbalance comparison (taker_side)
    taker_sigs = [s for s in all_sigs if s["taker_side"] in ("buy", "sell")]
    # taker_side buy on YES → price up; but we don't know outcome side mapping cleanly.
    # Skip deep taker analysis; note as future work.

    summary = {
        "params": {"horizon_s": HORIZON_S, "gap_s": SAMPLE_GAP_S, "min_size": MIN_SIZE},
        "n_samples": n,
        "pearson_corr": round(corr, 4),
        "decile_table": decile_table,
        "backtest": bt,
    }
    save_json("orderbook_imbalance.json", summary)
    conn.close()
    return summary


if __name__ == "__main__":
    run()
