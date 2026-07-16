"""Match-point market pricing edge.

For each match point (point that could end the match), find the market price
of the player-with-match-point's YES at that timestamp. Compare to the realized
conversion rate (did they win the match from there). If market under-prices the
conversion, buying the match-point player's YES is +EV.

Two strategies tested:
  A) buy_mp_player: buy the player who HAS the match point (favorite to finish).
  B) buy_saver:    buy the opponent (comeback bet, cheap when MP against them).

Also splits by: server (is the MP player serving?), tiebreak vs regular,
match-point number (1st/2nd/3rd... — later MPs convert more).
"""

import json
import statistics
import sys
from collections import Counter
from pathlib import Path

# reuse match-point detection from research/match_points.py
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from match_points import Point, analyze_match  # noqa: E402

from db import connect, log, save_json

FEE_CENTS = 0.01
STAKE = 100.0


def load_points(conn, match_ticker):
    rows = conn.execute("""
        SELECT set_number, game_number, point_number, server, scorer,
               home_points, away_points, home_games, away_games,
               home_set_games, away_set_games, is_tiebreak, is_break_point, ts_ms
        FROM points
        WHERE match_ticker=? AND ts_ms IS NOT NULL
        ORDER BY ts_ms
    """, (match_ticker,)).fetchall()
    return [Point(
        set_number=r["set_number"], game_number=r["game_number"],
        point_number=r["point_number"], server=r["server"], scorer=r["scorer"],
        home_points=r["home_points"], away_points=r["away_points"],
        home_games=r["home_games"], away_games=r["away_games"],
        home_set_games=r["home_set_games"], away_set_games=r["away_set_games"],
        is_tiebreak=r["is_tiebreak"], ts_ms=r["ts_ms"],
    ) for r in rows]


def market_for_player(conn, match_ticker, player):
    """player: 1=home, 2=away. Return (market_ticker, player_name) for that side.

    FlashScore stores 'Vukic A.' while Kalshi markets store 'Aleksandar Vukic'.
    Match by FlashScore last name (first token, lowercased) against any token
    in the Kalshi player_name.
    """
    row = conn.execute("""
        SELECT fs.home_player, fs.away_player
        FROM flashscore_matches fs
        WHERE fs.event_ticker=?
    """, (match_ticker,)).fetchone()
    if not row:
        return None, None
    name = row["home_player"] if player == 1 else row["away_player"]
    if not name:
        return None, None
    last = name.split()[0].lower().rstrip(".")
    mkts = conn.execute("""
        SELECT market_ticker, player_name FROM markets
        WHERE event_ticker=?
    """, (match_ticker,)).fetchall()
    for m in mkts:
        toks = [t.lower() for t in m["player_name"].split()]
        if last in toks:
            return m["market_ticker"], m["player_name"]
    return None, None


def price_near(conn, ticker, ts_ms, window_ms=5000):
    """Avg price within +/- window_ms of ts_ms."""
    row = conn.execute("""
        SELECT AVG(price) FROM ticks
        WHERE market_ticker=? AND price IS NOT NULL
          AND ts BETWEEN ? AND ?
    """, (ticker, ts_ms - window_ms, ts_ms + window_ms)).fetchone()
    return row[0]


def best_bid_near(conn, ticker, ts_ms, window_ms=5000):
    """Max yes_bid in window — conservative buy fill."""
    row = conn.execute("""
        SELECT MAX(yes_bid) FROM ticks
        WHERE market_ticker=? AND yes_bid IS NOT NULL
          AND ts BETWEEN ? AND ?
    """, (ticker, ts_ms - window_ms, ts_ms + window_ms)).fetchone()
    return row[0]


def pnl(buy, settle, stake=STAKE):
    if not buy or buy <= 0:
        return None
    shares = stake / buy
    return shares * settle - shares * FEE_CENTS - stake


def run():
    conn = connect()
    # gold set: settled + points + ticks
    tickers = [r["match_ticker"] for r in conn.execute("""
        SELECT DISTINCT p.match_ticker
        FROM points p
        WHERE p.ts_ms IS NOT NULL
          AND EXISTS (SELECT 1 FROM markets m, ticks t
                      WHERE m.event_ticker=p.match_ticker
                        AND t.market_ticker=m.market_ticker
                        AND m.status='finalized')
        ORDER BY p.match_ticker
    """).fetchall()]
    log(f"gold set matches: {len(tickers)}")

    records = []
    for tk in tickers:
        pts = load_points(conn, tk)
        if len(pts) < 10:
            continue
        res = analyze_match(tk, pts)
        if not res or not res.match_points:
            continue
        # MP player market
        for i, mp in enumerate(res.match_points):
            mkt, name = market_for_player(conn, tk, mp.player)
            if not mkt:
                continue
            opp_mkt, opp_name = market_for_player(conn, tk, 2 if mp.player == 1 else 1)
            price = price_near(conn, mkt, mp.ts_ms)
            bid = best_bid_near(conn, mkt, mp.ts_ms)
            opp_price = price_near(conn, opp_mkt, mp.ts_ms) if opp_mkt else None
            # settle: did MP player win the match?
            settle_mp = 1.0 if mp.player == res.winner else 0.0
            settle_opp = 1.0 - settle_mp
            records.append({
                "match": tk,
                "mp_idx": i + 1,
                "mp_player": mp.player,
                "server": mp.server,
                "is_tiebreak": mp.is_tiebreak,
                "converted": mp.converted,
                "context": mp.context,
                "mp_market": mkt,
                "mp_price": round(price, 4) if price is not None else None,
                "mp_bid": round(bid, 4) if bid is not None else None,
                "opp_market": opp_mkt,
                "opp_price": round(opp_price, 4) if opp_price is not None else None,
                "settle_mp": settle_mp,
                "settle_opp": settle_opp,
            })

    log(f"match-point records with market data: {len(records)}")
    # drop records missing price
    recs = [r for r in records if r["mp_price"] is not None]
    log(f"records with mp_price: {len(recs)}")

    def backtest(items, buy_key, settle_key, label, price_cap=1.0, price_floor=0.0):
        trades = []
        for r in items:
            p = r[buy_key]
            if p is None or p < price_floor or p > price_cap:
                continue
            p_l = pnl(p, r[settle_key])
            if p_l is None:
                continue
            trades.append({"match": r["match"], "buy": p, "settle": r[settle_key],
                           "pnl": round(p_l, 2), "roi": round(p_l / STAKE * 100, 2),
                           "won": r[settle_key] > 0.5})
        if not trades:
            return None
        pnls = [t["pnl"] for t in trades]
        rois = [t["roi"] for t in trades]
        wins = sum(1 for t in trades if t["won"])
        s = {
            "label": label, "n": len(trades), "wins": wins,
            "hit_rate": round(wins / len(trades), 4),
            "total_pnl": round(sum(pnls), 2),
            "avg_pnl": round(statistics.mean(pnls), 2),
            "avg_roi": round(statistics.mean(rois), 2),
            "std_roi": round(statistics.pstdev(rois), 2) if len(rois) > 1 else 0,
            "avg_buy": round(statistics.mean([t["buy"] for t in trades]), 4),
        }
        s["edge_vs_implied"] = round(s["hit_rate"] - s["avg_buy"], 4)
        s["sharpe"] = round(s["avg_pnl"] / s["std_roi"], 3) if s["std_roi"] else None
        log(f"[{label}] n={s['n']} hit={s['hit_rate']} total=${s['total_pnl']} "
            f"avg=${s['avg_pnl']} roi={s['avg_roi']}% buy@{s['avg_buy']} "
            f"edge={s['edge_vs_implied']} sharpe={s['sharpe']}")
        return {"summary": s, "trades": trades}

    out = {"records": recs, "strategies": {}}

    # A) buy MP player's YES (finish the match)
    out["strategies"]["buy_mp_player_all"] = backtest(recs, "mp_price", "settle_mp", "buy_mp_all")
    out["strategies"]["buy_mp_player_serving"] = backtest(
        [r for r in recs if r["server"] == r["mp_player"]],
        "mp_price", "settle_mp", "buy_mp_serving")
    out["strategies"]["buy_mp_player_returning"] = backtest(
        [r for r in recs if r["server"] != r["mp_player"]],
        "mp_price", "settle_mp", "buy_mp_returning")
    out["strategies"]["buy_mp_player_1st"] = backtest(
        [r for r in recs if r["mp_idx"] == 1], "mp_price", "settle_mp", "buy_mp_1st")
    out["strategies"]["buy_mp_player_later"] = backtest(
        [r for r in recs if r["mp_idx"] >= 2], "mp_price", "settle_mp", "buy_mp_later")
    # only cheap MPs (market still uncertain): price < 0.90
    out["strategies"]["buy_mp_cheap_<90c"] = backtest(
        recs, "mp_price", "settle_mp", "buy_mp_<90c", price_cap=0.90)
    out["strategies"]["buy_mp_cheap_<70c"] = backtest(
        recs, "mp_price", "settle_mp", "buy_mp_<70c", price_cap=0.70)

    # B) buy saver (opponent comeback) — opponent YES at MP moment
    opp_recs = [r for r in recs if r["opp_price"] is not None]
    out["strategies"]["buy_saver_all"] = backtest(opp_recs, "opp_price", "settle_opp", "buy_saver_all")
    out["strategies"]["buy_saver_cheap_<10c"] = backtest(
        opp_recs, "opp_price", "settle_opp", "buy_saver_<10c", price_cap=0.10)
    out["strategies"]["buy_saver_cheap_<5c"] = backtest(
        opp_recs, "opp_price", "settle_opp", "buy_saver_<5c", price_cap=0.05)

    # conversion rate stats
    conv = Counter()
    for r in recs:
        conv["total"] += 1
        if r["converted"]:
            conv["converted"] += 1
        key = f"server_{'self' if r['server']==r['mp_player'] else 'opp'}"
        conv[key] += 1
        if r["converted"]:
            conv[key + "_win"] += 1
    log(f"conversion: {conv['converted']}/{conv['total']} "
        f"= {conv['converted']/max(conv['total'],1):.3f}")
    log(f"  serving self: {conv.get('server_self_win',0)}/{conv.get('server_self',0)}")
    log(f"  serving opp:  {conv.get('server_opp_win',0)}/{conv.get('server_opp',0)}")
    out["conversion_stats"] = dict(conv)

    save_json("match_point_edge.json", out)
    conn.close()
    return out


if __name__ == "__main__":
    run()
