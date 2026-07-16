"""Spike reversion: do large short-window price moves revert or continue?

For each market, slide a window over ticks. When |price_move| over W seconds
exceeds a threshold, record the move and the price T seconds forward.
Classify: reverted (moved back >50% of spike) vs continued.

Strategy test: fade the spike (sell the spike-top / buy the spike-bottom)
and hold T seconds. PnL vs hold-and-hope.

Also separates up-spikes vs down-spikes — tennis momentum may be asymmetric
(a break of serve is informational, not noise).
"""

import json
import statistics
from collections import defaultdict

from db import connect, log, save_json

WINDOW_S = 30          # measure move over 30s
HOLD_S = 60            # hold 60s then mark-to-market
SPIKE_CENTS = 10       # 10c+ move = spike
SAMPLE_GAP_S = 10      # sample a new spike candidate every 10s to avoid overlap


def load_markets(conn):
    return [dict(r) for r in conn.execute("""
        SELECT m.market_ticker, m.event_ticker, m.player_name, m.result,
               m.settlement_value, m.close_ts, m.status
        FROM markets m
        WHERE m.status='finalized'
          AND (SELECT COUNT(*) FROM ticks t WHERE t.market_ticker=m.market_ticker
               AND t.price IS NOT NULL) >= 200
    """).fetchall()]


def load_prices(conn, ticker):
    """Return list of (ts, price) sorted, price not null."""
    rows = conn.execute("""
        SELECT ts, price FROM ticks
        WHERE market_ticker=? AND price IS NOT NULL
        ORDER BY ts
    """, (ticker,)).fetchall()
    return [(r["ts"], r["price"]) for r in rows]


def find_spikes(prices, window_ms, hold_ms, spike_cents, gap_ms):
    """Find spike events. Returns list of dicts."""
    if len(prices) < 10:
        return []
    ts = [p[0] for p in prices]
    px = [p[1] for p in prices]
    spikes = []
    last_sample = -10**12
    n = len(prices)
    for i in range(n):
        t_now = ts[i]
        if t_now - last_sample < gap_ms:
            continue
        # find price ~window_ms earlier
        target = t_now - window_ms
        # binary-ish: linear scan from last j
        j = i
        while j > 0 and ts[j] > target:
            j -= 1
        if j == i:
            continue
        p_prev = px[j]
        p_now = px[i]
        move = p_now - p_prev
        if abs(move) < spike_cents / 100.0:
            continue
        # find price ~hold_ms later
        target_fwd = t_now + hold_ms
        k = i
        while k < n - 1 and ts[k] < target_fwd:
            k += 1
        p_fwd = px[k]
        # reversion: did price move back toward p_prev by >50% of spike?
        reversion = (p_prev - p_fwd) if move > 0 else (p_fwd - p_prev)
        reverted = reversion > 0.5 * abs(move)
        spikes.append({
            "ts": t_now, "p_prev": round(p_prev, 4), "p_now": round(p_now, 4),
            "p_fwd": round(p_fwd, 4), "move": round(move, 4),
            "fwd_move": round(p_fwd - p_now, 4),
            "reverted": reverted, "direction": "up" if move > 0 else "down",
        })
        last_sample = t_now
    return spikes


def fade_pnl(spike):
    """Fade: if up-spike, sell at p_now, buy back at p_fwd. PnL = p_now - p_fwd.
    If down-spike, buy at p_now, sell at p_fwd. PnL = p_fwd - p_now.
    Per $1 notional (1 share)."""
    if spike["direction"] == "up":
        return spike["p_now"] - spike["p_fwd"]
    return spike["p_fwd"] - spike["p_now"]


def run():
    conn = connect()
    markets = load_markets(conn)
    log(f"finalized markets with 200+ priced ticks: {len(markets)}")

    all_spikes = []
    per_market = []
    for m in markets:
        prices = load_prices(conn, m["market_ticker"])
        sp = find_spikes(prices, WINDOW_S * 1000, HOLD_S * 1000,
                         SPIKE_CENTS, SAMPLE_GAP_S * 1000)
        if not sp:
            continue
        for s in sp:
            s["market"] = m["market_ticker"]
            s["event"] = m["event_ticker"]
            s["result"] = m["result"]
            s["fade_pnl"] = round(fade_pnl(s), 4)
        all_spikes.extend(sp)
        per_market.append({"market": m["market_ticker"], "n_spikes": len(sp)})

    log(f"total spikes (>{SPIKE_CENTS}c over {WINDOW_S}s, hold {HOLD_S}s): {len(all_spikes)}")
    if not all_spikes:
        save_json("spike_reversion.json", {"spikes": [], "summary": {}})
        return

    up = [s for s in all_spikes if s["direction"] == "up"]
    down = [s for s in all_spikes if s["direction"] == "down"]

    def summ(items, label):
        if not items:
            log(f"[{label}] none")
            return None
        rev = sum(1 for s in items if s["reverted"])
        fades = [s["fade_pnl"] for s in items]
        s = {
            "label": label, "n": len(items),
            "reverted": rev, "reversion_rate": round(rev / len(items), 4),
            "avg_move": round(statistics.mean([s["move"] for s in items]), 4),
            "avg_fwd_move": round(statistics.mean([s["fwd_move"] for s in items]), 4),
            "fade_total": round(sum(fades), 2),
            "fade_avg": round(statistics.mean(fades), 4),
            "fade_median": round(statistics.median(fades), 4),
            "fade_win_rate": round(sum(1 for f in fades if f > 0) / len(fades), 4),
            "fade_std": round(statistics.pstdev(fades), 4) if len(fades) > 1 else 0,
        }
        s["sharpe"] = round(s["fade_avg"] / s["fade_std"], 3) if s["fade_std"] else None
        log(f"[{label}] n={s['n']} revert={s['reversion_rate']} "
            f"avg_move={s['avg_move']} fwd_move={s['avg_fwd_move']} "
            f"fade_avg={s['fade_avg']} win={s['fade_win_rate']} "
            f"sharpe={s['sharpe']}")
        return s

    summary = {
        "params": {"window_s": WINDOW_S, "hold_s": HOLD_S,
                   "spike_cents": SPIKE_CENTS, "gap_s": SAMPLE_GAP_S},
        "all": summ(all_spikes, "all"),
        "up": summ(up, "up_spikes"),
        "down": summ(down, "down_spikes"),
        # split by winner/loser market
        "winner_market": summ([s for s in all_spikes if s["result"] == "yes"], "winner_mkt"),
        "loser_market": summ([s for s in all_spikes if s["result"] == "no"], "loser_mkt"),
    }
    # bigger spikes
    big = [s for s in all_spikes if abs(s["move"]) >= 0.20]
    summary["big_>20c"] = summ(big, "big_>20c")
    huge = [s for s in all_spikes if abs(s["move"]) >= 0.30]
    summary["huge_>30c"] = summ(huge, "huge_>30c")

    save_json("spike_reversion.json", {"summary": summary, "spikes": all_spikes[:500]})
    conn.close()
    return summary


if __name__ == "__main__":
    run()
