"""Nothing Ever Happens: fade the longshot near close.

Hypothesis: tennis match-winners near close are nearly decided; the market
under-prices the heavy favorite's YES (or equivalently the loser's NO).
Buy the favorite's YES at T-Xmin, hold to settlement ($1 if right, $0 if wrong).

We backtest across all finalized events with both markets + enough ticks.
Reports per-event PnL with fees, hit rate, ROI distribution, and a naive
baseline (buy whichever side is currently leading in price, same timing).

Also tests the symmetric "fade from the other side": sell the loser's YES
if it's still bid above 0 (resolution to 0). We approximate via buying No.
"""

import json
import statistics
from datetime import datetime, timezone

from db import connect, log, save_json

# Kalshi fee model assumption: 0 fee on entry, settlement fee on profit.
# Per Kalshi docs: 0% maker / small taker; we model conservative 1c/share
# round-trip + 0% on settlement. Adjust FEE_CENTS to test sensitivity.
FEE_CENTS_PER_SHARE = 0.01
STAKE_DOLLARS = 100.0  # fixed stake per trade for PnL aggregation


def fmt_ts(ms):
    return datetime.fromtimestamp(ms / 1000, tz=timezone.utc).isoformat() if ms else None


def load_finalized(conn):
    rows = conn.execute("""
        SELECT e.event_ticker, e.series_ticker, e.title,
               myes.market_ticker AS yes_ticker, myes.player_name AS yes_player,
               myes.result AS yes_result, myes.settlement_value AS yes_settle,
               mno.market_ticker AS no_ticker, mno.player_name AS no_player,
               mno.result AS no_result, mno.settlement_value AS no_settle,
               myes.close_ts
        FROM events e
        JOIN markets myes ON e.event_ticker=myes.event_ticker AND myes.result='yes'
        JOIN markets mno  ON e.event_ticker=mno.event_ticker AND mno.result='no'
        WHERE myes.status='finalized' AND mno.status='finalized'
          AND myes.close_ts IS NOT NULL
        ORDER BY e.event_ticker
    """).fetchall()
    return [dict(r) for r in rows]


def price_at(conn, ticker, close_ts, before_s, after_s=0):
    """Avg price in [close_ts - before_s*1000, close_ts - after_s*1000].

    after_s excludes the final after_s seconds (often illiquid/halted).
    Clamped so the window never inverts for short before_s.
    """
    after_s = min(after_s, max(0, before_s - 5))
    row = conn.execute("""
        SELECT AVG(price) FROM ticks
        WHERE market_ticker=? AND price IS NOT NULL
          AND ts BETWEEN ? AND ?
    """, (ticker, close_ts - before_s * 1000, close_ts - after_s * 1000)).fetchone()
    return row[0]


def best_bid_at(conn, ticker, close_ts, before_s):
    """Max yes_bid in window — conservative fill for a buy."""
    row = conn.execute("""
        SELECT MAX(yes_bid) FROM ticks
        WHERE market_ticker=? AND yes_bid IS NOT NULL
          AND ts BETWEEN ? AND ?
    """, (ticker, close_ts - before_s * 1000, close_ts - 60_000)).fetchone()
    return row[0]


def trade_pnl(buy_price, settle_value, stake):
    """Buy `stake/buy_price` shares at buy_price, settle at settle_value."""
    if not buy_price or buy_price <= 0:
        return None
    shares = stake / buy_price
    gross = shares * settle_value
    fees = shares * FEE_CENTS_PER_SHARE
    return gross - fees - stake


def backtest_side(events, conn, window_s, threshold, pick="favorite"):
    """Buy one side at T-window_s. pick='favorite' = higher-priced side;
    'underdog' = lower-priced side. threshold filters min price to enter.
    No hindsight: side chosen by current market price, not by result.
    """
    trades = []
    for e in events:
        p_yes = price_at(conn, e["yes_ticker"], e["close_ts"], window_s, 60)
        p_no = price_at(conn, e["no_ticker"], e["close_ts"], window_s, 60)
        if p_yes is None and p_no is None:
            continue
        yes_settle = float(e["yes_settle"]) if e["yes_settle"] is not None else (1.0 if e["yes_result"] == "yes" else 0.0)
        no_settle = float(e["no_settle"]) if e["no_settle"] is not None else (1.0 if e["no_result"] == "yes" else 0.0)
        # favorite = higher price now; underdog = lower
        if (p_yes or 0) >= (p_no or 0):
            fav_ticker, fav_price, fav_settle, fav_player = "yes", p_yes, yes_settle, e["yes_player"]
            dog_ticker, dog_price, dog_settle, dog_player = "no", p_no, no_settle, e["no_player"]
        else:
            fav_ticker, fav_price, fav_settle, fav_player = "no", p_no, no_settle, e["no_player"]
            dog_ticker, dog_price, dog_settle, dog_player = "yes", p_yes, yes_settle, e["yes_player"]
        if pick == "favorite":
            side, buy_price, settle, player = fav_ticker, fav_price, fav_settle, fav_player
        else:
            side, buy_price, settle, player = dog_ticker, dog_price, dog_settle, dog_player
        if buy_price is None or buy_price < threshold:
            continue
        pnl = trade_pnl(buy_price, settle, STAKE_DOLLARS)
        if pnl is None:
            continue
        trades.append({
            "event": e["event_ticker"],
            "series": e["series_ticker"],
            "side": side,
            "player": player,
            "buy_price": round(buy_price, 4),
            "settle": settle,
            "pnl": round(pnl, 2),
            "roi_pct": round(pnl / STAKE_DOLLARS * 100, 2),
            "won": settle > 0.5,
        })
    return trades


def summarize(trades, label):
    if not trades:
        log(f"[{label}] no trades")
        return None
    pnls = [t["pnl"] for t in trades]
    rois = [t["roi_pct"] for t in trades]
    wins = [t for t in trades if t["won"]]
    s = {
        "label": label,
        "n": len(trades),
        "wins": len(wins),
        "hit_rate": round(len(wins) / len(trades), 4),
        "total_pnl": round(sum(pnls), 2),
        "avg_pnl": round(statistics.mean(pnls), 2),
        "median_pnl": round(statistics.median(pnls), 2),
        "avg_roi_pct": round(statistics.mean(rois), 2),
        "std_roi": round(statistics.pstdev(rois), 2) if len(rois) > 1 else 0,
        "max_pnl": round(max(pnls), 2),
        "min_pnl": round(min(pnls), 2),
        "avg_buy_price": round(statistics.mean([t["buy_price"] for t in trades]), 4),
    }
    # risk-adj: Sharpe-like per trade (mean/std)
    s["sharpe_per_trade"] = round(s["avg_pnl"] / s["std_roi"], 3) if s["std_roi"] else None
    log(f"[{label}] n={s['n']} hit={s['hit_rate']} total=${s['total_pnl']} "
        f"avg=${s['avg_pnl']} roi={s['avg_roi_pct']}% std={s['std_roi']} "
        f"sharpe={s['sharpe_per_trade']} buy@{s['avg_buy_price']}")
    return s


def run():
    conn = connect()
    events = load_finalized(conn)
    log(f"finalized events with both markets: {len(events)}")

    results = {}
    for window in [600, 300, 180, 120, 60, 30]:
        for thresh in [0.0, 0.50, 0.70, 0.85, 0.90, 0.95]:
            trades = backtest_side(events, conn, window, thresh, pick="favorite")
            key = f"fav_T-{window}s_thr{thresh:.2f}"
            s = summarize(trades, key)
            if s:
                # edge = realized hit rate - implied prob (avg buy price)
                s["implied_prob"] = s["avg_buy_price"]
                s["edge_vs_implied"] = round(s["hit_rate"] - s["implied_prob"], 4)
                log(f"  edge_vs_implied={s['edge_vs_implied']}")
            results[key] = {"summary": s, "trades": trades}

    # underdog contrast at T-300s (buy the lower-priced side)
    for thresh in [0.0, 0.05, 0.10, 0.20, 0.30]:
        trades = backtest_side(events, conn, 300, thresh, pick="underdog")
        key = f"dog_T-300s_thr{thresh:.2f}"
        results[key] = {"summary": summarize(trades, key), "trades": trades}

    save_json("nothing_happens.json", results)
    conn.close()
    return results


if __name__ == "__main__":
    run()
