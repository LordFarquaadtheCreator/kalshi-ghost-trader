"""Deep data exploration for strategy design.

Analyzes:
1. Price trajectories near market close
2. Set-point conversion rates + market pricing
3. Break-of-serve impact on market price
4. Series-level differences (ATP vs ITF vs Challenger)
5. Set score transitions (1-0, 0-1, 1-1) and price behavior
6. Price drift / momentum patterns
7. Favorite dominance by time window
8. Serve hold rates and market implications

Output: out/explore_results.json + console summary
"""

import json
import statistics
from collections import defaultdict, Counter
from datetime import datetime, timezone

from db import connect, log, save_json


def load_finalized_with_both(conn):
    rows = conn.execute("""
        SELECT e.event_ticker, e.series_ticker, e.title, e.coverage,
               myes.market_ticker AS yes_ticker, myes.player_name AS yes_player,
               myes.result AS yes_result, myes.settlement_value AS yes_settle,
               myes.close_ts, myes.open_ts, myes.occurrence_ts,
               mno.market_ticker AS no_ticker, mno.player_name AS no_player,
               mno.result AS no_result, mno.settlement_value AS no_settle
        FROM events e
        JOIN markets myes ON e.event_ticker=myes.event_ticker AND myes.result='yes'
        JOIN markets mno  ON e.event_ticker=mno.event_ticker AND mno.result='no'
        WHERE myes.status='finalized' AND mno.status='finalized'
          AND myes.close_ts IS NOT NULL
        ORDER BY e.event_ticker
    """).fetchall()
    return [dict(r) for r in rows]


def price_at(conn, ticker, close_ts, before_s, after_s=60):
    after_s = min(after_s, max(0, before_s - 5))
    row = conn.execute("""
        SELECT AVG(price) FROM ticks
        WHERE market_ticker=? AND price IS NOT NULL
          AND ts BETWEEN ? AND ?
    """, (ticker, close_ts - before_s * 1000, close_ts - after_s * 1000)).fetchone()
    return row[0]


def load_points_for_match(conn, match_ticker):
    rows = conn.execute("""
        SELECT set_number, game_number, point_number, server, scorer,
               home_points, away_points, home_games, away_games,
               is_tiebreak, is_break_point, is_match_point, is_set_point, ts_ms
        FROM points
        WHERE match_ticker=? AND ts_ms IS NOT NULL
        ORDER BY ts_ms
    """, (match_ticker,)).fetchall()
    return [dict(r) for r in rows]


def explore_price_trajectories(conn, events):
    log("\n=== 1. PRICE TRAJECTORIES NEAR CLOSE ===")
    windows = [600, 300, 180, 120, 60, 30]
    results = {}

    for w in windows:
        fav_prices = []
        dog_prices = []
        spreads = []
        for e in events:
            p_yes = price_at(conn, e["yes_ticker"], e["close_ts"], w, 60)
            p_no = price_at(conn, e["no_ticker"], e["close_ts"], w, 60)
            if p_yes is not None and p_no is not None:
                fav = max(p_yes, p_no)
                dog = min(p_yes, p_no)
                fav_prices.append(fav)
                dog_prices.append(dog)
                spreads.append(fav - dog)

        if fav_prices:
            results[f"T-{w}s"] = {
                "n": len(fav_prices),
                "fav_avg": round(statistics.mean(fav_prices), 4),
                "fav_median": round(statistics.median(fav_prices), 4),
                "dog_avg": round(statistics.mean(dog_prices), 4),
                "dog_median": round(statistics.median(dog_prices), 4),
                "spread_avg": round(statistics.mean(spreads), 4),
                "spread_median": round(statistics.median(spreads), 4),
                "fav_gt_90": sum(1 for p in fav_prices if p >= 0.90),
                "fav_gt_70": sum(1 for p in fav_prices if p >= 0.70),
                "dog_lt_10": sum(1 for p in dog_prices if p < 0.10),
                "dog_lt_05": sum(1 for p in dog_prices if p < 0.05),
            }
            r = results[f"T-{w}s"]
            log(f"  T-{w}s: n={r['n']} fav_avg={r['fav_avg']} dog_avg={r['dog_avg']} "
                f"spread={r['spread_avg']} fav>=90c={r['fav_gt_90']} dog<10c={r['dog_lt_10']}")

    return results


def explore_set_points(conn, events):
    log("\n=== 2. SET-POINT CONVERSION + MARKET PRICING ===")
    set_point_records = []

    for e in events:
        pts = load_points_for_match(conn, e["event_ticker"])
        if len(pts) < 10:
            continue

        sets_home = 0
        sets_away = 0
        last_set_num = 0
        last_home_games = 0
        last_away_games = 0
        last_scorer = 0

        for p in pts:
            if p["set_number"] > last_set_num and last_set_num > 0:
                if last_home_games > last_away_games:
                    sets_home += 1
                elif last_away_games > last_home_games:
                    sets_away += 1
                elif last_scorer == 1:
                    sets_home += 1
                elif last_scorer == 2:
                    sets_away += 1

            last_set_num = p["set_number"]
            last_home_games = p["home_games"]
            last_away_games = p["away_games"]
            last_scorer = p["scorer"]

            if p["is_tiebreak"]:
                continue

            home_can_win_game = (
                p["home_points"] == "A" or
                (p["home_points"] == "40" and p["away_points"] not in ("40", "A"))
            )
            away_can_win_game = (
                p["away_points"] == "A" or
                (p["away_points"] == "40" and p["home_points"] not in ("40", "A"))
            )

            home_can_win_set = (
                home_can_win_game and
                p["home_games"] >= 5 and
                p["home_games"] > p["away_games"]
            )
            away_can_win_set = (
                away_can_win_game and
                p["away_games"] >= 5 and
                p["away_games"] > p["home_games"]
            )

            is_match_point = (
                (home_can_win_set and sets_home == 1) or
                (away_can_win_set and sets_away == 1)
            )

            if home_can_win_set or away_can_win_set:
                sp_player = 1 if home_can_win_set else 2
                server = p["server"]
                is_serving = (sp_player == server)
                won_point = (p["scorer"] == sp_player)

                mkt_ticker = e["yes_ticker"] if sp_player == 1 else e["no_ticker"]
                row = conn.execute("""
                    SELECT AVG(price) FROM ticks
                    WHERE market_ticker=? AND price IS NOT NULL
                      AND ts BETWEEN ? AND ?
                """, (mkt_ticker, p["ts_ms"] - 5000, p["ts_ms"] + 5000)).fetchone()
                mkt_price = row[0] if row else None

                set_point_records.append({
                    "match": e["event_ticker"],
                    "set_number": p["set_number"],
                    "sp_player": sp_player,
                    "is_serving": is_serving,
                    "is_match_point": is_match_point,
                    "won_point": won_point,
                    "mkt_price": round(mkt_price, 4) if mkt_price else None,
                    "home_games": p["home_games"],
                    "away_games": p["away_games"],
                })

    if not set_point_records:
        log("  No set points found")
        return {}

    total = len(set_point_records)
    won = sum(1 for r in set_point_records if r["won_point"])
    serving = [r for r in set_point_records if r["is_serving"]]
    returning = [r for r in set_point_records if not r["is_serving"]]
    mp_recs = [r for r in set_point_records if r["is_match_point"]]
    sp_only = [r for r in set_point_records if not r["is_match_point"]]

    results = {
        "total_set_points": total,
        "conversion_rate": round(won / total, 4),
        "serving": {
            "n": len(serving),
            "conversion": round(sum(1 for r in serving if r["won_point"]) / max(len(serving), 1), 4),
        },
        "returning": {
            "n": len(returning),
            "conversion": round(sum(1 for r in returning if r["won_point"]) / max(len(returning), 1), 4),
        },
        "match_points": {
            "n": len(mp_recs),
            "conversion": round(sum(1 for r in mp_recs if r["won_point"]) / max(len(mp_recs), 1), 4),
        },
        "set_points_only": {
            "n": len(sp_only),
            "conversion": round(sum(1 for r in sp_only if r["won_point"]) / max(len(sp_only), 1), 4),
        },
    }

    sp_with_price = [r for r in set_point_records if r["mkt_price"] is not None]
    if sp_with_price:
        results["with_price"] = {
            "n": len(sp_with_price),
            "avg_price": round(statistics.mean([r["mkt_price"] for r in sp_with_price]), 4),
            "conversion": round(sum(1 for r in sp_with_price if r["won_point"]) / len(sp_with_price), 4),
        }
        results["with_price"]["edge"] = round(
            results["with_price"]["conversion"] - results["with_price"]["avg_price"], 4
        )

        serve_p = [r for r in sp_with_price if r["is_serving"]]
        ret_p = [r for r in sp_with_price if not r["is_serving"]]
        if serve_p:
            results["serving_with_price"] = {
                "n": len(serve_p),
                "avg_price": round(statistics.mean([r["mkt_price"] for r in serve_p]), 4),
                "conversion": round(sum(1 for r in serve_p if r["won_point"]) / len(serve_p), 4),
            }
            results["serving_with_price"]["edge"] = round(
                results["serving_with_price"]["conversion"] - results["serving_with_price"]["avg_price"], 4
            )
        if ret_p:
            results["returning_with_price"] = {
                "n": len(ret_p),
                "avg_price": round(statistics.mean([r["mkt_price"] for r in ret_p]), 4),
                "conversion": round(sum(1 for r in ret_p if r["won_point"]) / len(ret_p), 4),
            }
            results["returning_with_price"]["edge"] = round(
                results["returning_with_price"]["conversion"] - results["returning_with_price"]["avg_price"], 4
            )

    log(f"  Total set points: {total}")
    log(f"  Conversion: {results['conversion_rate']}")
    log(f"  Serving: n={results['serving']['n']} conv={results['serving']['conversion']}")
    log(f"  Returning: n={results['returning']['n']} conv={results['returning']['conversion']}")
    log(f"  Match points: n={results['match_points']['n']} conv={results['match_points']['conversion']}")
    log(f"  Set points only: n={results['set_points_only']['n']} conv={results['set_points_only']['conversion']}")
    if "with_price" in results:
        log(f"  With price: n={results['with_price']['n']} avg_price={results['with_price']['avg_price']} "
            f"conv={results['with_price']['conversion']} edge={results['with_price']['edge']}")
    if "serving_with_price" in results:
        log(f"  Serving w/price: n={results['serving_with_price']['n']} "
            f"avg_price={results['serving_with_price']['avg_price']} "
            f"conv={results['serving_with_price']['conversion']} "
            f"edge={results['serving_with_price']['edge']}")
    if "returning_with_price" in results:
        log(f"  Returning w/price: n={results['returning_with_price']['n']} "
            f"avg_price={results['returning_with_price']['avg_price']} "
            f"conv={results['returning_with_price']['conversion']} "
            f"edge={results['returning_with_price']['edge']}")

    return results


def explore_break_impact(conn, events):
    log("\n=== 3. BREAK OF SERVE IMPACT ===")
    break_records = []

    for e in events:
        pts = load_points_for_match(conn, e["event_ticker"])
        if len(pts) < 10:
            continue

        for i, p in enumerate(pts):
            if p["scorer"] != p["server"] and p["scorer"] != 0:
                is_game_end = False
                if i + 1 < len(pts):
                    next_p = pts[i + 1]
                    if (next_p["game_number"] != p["game_number"] or
                        next_p["set_number"] != p["set_number"]):
                        is_game_end = True
                else:
                    is_game_end = True

                if not is_game_end:
                    continue

                breaker = p["scorer"]
                mkt_ticker = e["yes_ticker"] if breaker == 1 else e["no_ticker"]

                row_before = conn.execute("""
                    SELECT AVG(price) FROM ticks
                    WHERE market_ticker=? AND price IS NOT NULL
                      AND ts BETWEEN ? AND ?
                """, (mkt_ticker, p["ts_ms"] - 10000, p["ts_ms"] - 1000)).fetchone()
                row_after = conn.execute("""
                    SELECT AVG(price) FROM ticks
                    WHERE market_ticker=? AND price IS NOT NULL
                      AND ts BETWEEN ? AND ?
                """, (mkt_ticker, p["ts_ms"] + 1000, p["ts_ms"] + 10000)).fetchone()

                p_before = row_before[0] if row_before and row_before[0] else None
                p_after = row_after[0] if row_after and row_after[0] else None

                if p_before and p_after:
                    break_records.append({
                        "match": e["event_ticker"],
                        "set": p["set_number"],
                        "game": p["game_number"],
                        "breaker": breaker,
                        "price_before": round(p_before, 4),
                        "price_after": round(p_after, 4),
                        "price_move": round(p_after - p_before, 4),
                    })

    if not break_records:
        log("  No break-of-serve records with price data")
        return {}

    moves = [r["price_move"] for r in break_records]
    results = {
        "n": len(break_records),
        "avg_move": round(statistics.mean(moves), 4),
        "median_move": round(statistics.median(moves), 4),
        "std_move": round(statistics.pstdev(moves), 4) if len(moves) > 1 else 0,
        "positive_moves": sum(1 for m in moves if m > 0),
        "negative_moves": sum(1 for m in moves if m < 0),
        "avg_move_cents": round(statistics.mean(moves) * 100, 2),
    }

    early = [r for r in break_records if r["set"] == 1 and r["game"] <= 3]
    late = [r for r in break_records if r["set"] >= 2 or r["game"] > 3]
    if early:
        em = [r["price_move"] for r in early]
        results["early_breaks"] = {
            "n": len(early),
            "avg_move": round(statistics.mean(em), 4),
            "avg_cents": round(statistics.mean(em) * 100, 2),
        }
    if late:
        lm = [r["price_move"] for r in late]
        results["late_breaks"] = {
            "n": len(late),
            "avg_move": round(statistics.mean(lm), 4),
            "avg_cents": round(statistics.mean(lm) * 100, 2),
        }

    log(f"  Total breaks: {results['n']}")
    log(f"  Avg price move: {results['avg_move']} ({results['avg_move_cents']}c)")
    log(f"  Median: {results['median_move']}")
    log(f"  Positive: {results['positive_moves']} Negative: {results['negative_moves']}")
    if "early_breaks" in results:
        log(f"  Early breaks: n={results['early_breaks']['n']} avg={results['early_breaks']['avg_cents']}c")
    if "late_breaks" in results:
        log(f"  Late breaks: n={results['late_breaks']['n']} avg={results['late_breaks']['avg_cents']}c")

    return results


def explore_series_differences(conn, events):
    log("\n=== 4. SERIES-LEVEL DIFFERENCES ===")
    series_data = defaultdict(lambda: {"events": [], "fav_prices": [], "dog_prices": []})

    for e in events:
        series = e["series_ticker"]
        series_data[series]["events"].append(e)

        p_yes = price_at(conn, e["yes_ticker"], e["close_ts"], 300, 60)
        p_no = price_at(conn, e["no_ticker"], e["close_ts"], 300, 60)
        if p_yes and p_no:
            series_data[series]["fav_prices"].append(max(p_yes, p_no))
            series_data[series]["dog_prices"].append(min(p_yes, p_no))

    results = {}
    for series, data in sorted(series_data.items()):
        if len(data["events"]) < 5:
            continue
        r = {
            "n_events": len(data["events"]),
            "n_with_price": len(data["fav_prices"]),
        }
        if data["fav_prices"]:
            r["fav_avg"] = round(statistics.mean(data["fav_prices"]), 4)
            r["dog_avg"] = round(statistics.mean(data["dog_prices"]), 4)
            r["spread_avg"] = round(r["fav_avg"] - r["dog_avg"], 4)
            r["fav_gt_90"] = sum(1 for p in data["fav_prices"] if p >= 0.90)
            r["upset_rate"] = round(
                sum(1 for p in data["dog_prices"] if p >= 0.50) / len(data["dog_prices"]), 4
            )
        results[series] = r
        log(f"  {series}: n={r['n_events']} priced={r.get('n_with_price', 0)} "
            f"fav_avg={r.get('fav_avg', 'N/A')} spread={r.get('spread_avg', 'N/A')} "
            f"upsets={r.get('upset_rate', 'N/A')}")

    return results


def explore_set_score_transitions(conn, events):
    log("\n=== 5. SET SCORE TRANSITIONS ===")
    transition_records = []

    for e in events:
        pts = load_points_for_match(conn, e["event_ticker"])
        if len(pts) < 10:
            continue

        sets_home = 0
        sets_away = 0
        last_set_num = 0
        last_home_games = 0
        last_away_games = 0
        last_scorer = 0
        last_ts = 0

        for p in pts:
            if p["set_number"] > last_set_num and last_set_num > 0:
                if last_home_games > last_away_games:
                    sets_home += 1
                elif last_away_games > last_home_games:
                    sets_away += 1
                elif last_scorer == 1:
                    sets_home += 1
                elif last_scorer == 2:
                    sets_away += 1

                score_key = f"{sets_home}-{sets_away}"
                set_winner = 1 if last_home_games > last_away_games or (
                    last_home_games == last_away_games and last_scorer == 1
                ) else 2
                mkt_ticker = e["yes_ticker"] if set_winner == 1 else e["no_ticker"]

                row = conn.execute("""
                    SELECT AVG(price) FROM ticks
                    WHERE market_ticker=? AND price IS NOT NULL
                      AND ts BETWEEN ? AND ?
                """, (mkt_ticker, last_ts, last_ts + 30000)).fetchone()
                p_after = row[0] if row and row[0] else None

                row2 = conn.execute("""
                    SELECT AVG(price) FROM ticks
                    WHERE market_ticker=? AND price IS NOT NULL
                      AND ts BETWEEN ? AND ?
                """, (mkt_ticker, last_ts - 30000, last_ts)).fetchone()
                p_before = row2[0] if row2 and row2[0] else None

                if p_before and p_after:
                    transition_records.append({
                        "match": e["event_ticker"],
                        "score": score_key,
                        "set_winner": set_winner,
                        "price_before": round(p_before, 4),
                        "price_after": round(p_after, 4),
                        "price_move": round(p_after - p_before, 4),
                    })

            last_set_num = p["set_number"]
            last_home_games = p["home_games"]
            last_away_games = p["away_games"]
            last_scorer = p["scorer"]
            last_ts = p["ts_ms"]

    if not transition_records:
        log("  No transition records with price data")
        return {}

    results = {}
    by_score = defaultdict(list)
    for r in transition_records:
        by_score[r["score"]].append(r)

    for score, recs in sorted(by_score.items()):
        moves = [r["price_move"] for r in recs]
        results[score] = {
            "n": len(recs),
            "avg_move": round(statistics.mean(moves), 4),
            "avg_cents": round(statistics.mean(moves) * 100, 2),
            "avg_price_after": round(statistics.mean([r["price_after"] for r in recs]), 4),
            "positive": sum(1 for m in moves if m > 0),
            "negative": sum(1 for m in moves if m < 0),
        }
        log(f"  Score {score}: n={results[score]['n']} avg_move={results[score]['avg_cents']}c "
            f"price_after={results[score]['avg_price_after']} "
            f"pos={results[score]['positive']} neg={results[score]['negative']}")

    return results


def explore_price_drift(conn, events):
    """6. Price drift: does price trend toward final outcome over time?"""
    log("\n=== 6. PRICE DRIFT TOWARD OUTCOME ===")
    drift_records = []

    for e in events:
        p_yes_600 = price_at(conn, e["yes_ticker"], e["close_ts"], 600, 60)
        p_yes_300 = price_at(conn, e["yes_ticker"], e["close_ts"], 300, 60)
        p_yes_60 = price_at(conn, e["yes_ticker"], e["close_ts"], 60, 30)

        if p_yes_600 is None or p_yes_300 is None or p_yes_60 is None:
            continue

        yes_won = e["yes_result"] == "yes"
        settle = 1.0 if yes_won else 0.0

        drift_records.append({
            "match": e["event_ticker"],
            "p_600": round(p_yes_600, 4),
            "p_300": round(p_yes_300, 4),
            "p_60": round(p_yes_60, 4),
            "settle": settle,
            "drift_600_to_300": round(p_yes_300 - p_yes_600, 4),
            "drift_300_to_60": round(p_yes_60 - p_yes_300, 4),
            "drift_600_to_settle": round(settle - p_yes_600, 4),
            "drift_300_to_settle": round(settle - p_yes_300, 4),
        })

    if not drift_records:
        log("  No drift records")
        return {}

    results = {
        "n": len(drift_records),
        "avg_drift_600_to_300": round(statistics.mean([r["drift_600_to_300"] for r in drift_records]), 4),
        "avg_drift_300_to_60": round(statistics.mean([r["drift_300_to_60"] for r in drift_records]), 4),
        "avg_abs_drift_600_to_300": round(statistics.mean([abs(r["drift_600_to_300"]) for r in drift_records]), 4),
        "avg_abs_drift_300_to_60": round(statistics.mean([abs(r["drift_300_to_60"]) for r in drift_records]), 4),
    }

    # Drift toward outcome: does price move toward settlement value?
    correct_600 = sum(1 for r in drift_records if abs(r["drift_600_to_settle"]) < abs(r["p_600"] - 0.5))
    correct_300 = sum(1 for r in drift_records if abs(r["drift_300_to_settle"]) < abs(r["p_300"] - 0.5))
    results["pct_closer_to_settle_from_600"] = round(correct_600 / len(drift_records), 4)
    results["pct_closer_to_settle_from_300"] = round(correct_300 / len(drift_records), 4)

    # Winners: does price increase toward 1.0?
    winners = [r for r in drift_records if r["settle"] == 1.0]
    losers = [r for r in drift_records if r["settle"] == 0.0]
    if winners:
        results["winners"] = {
            "n": len(winners),
            "avg_p_600": round(statistics.mean([r["p_600"] for r in winners]), 4),
            "avg_p_300": round(statistics.mean([r["p_300"] for r in winners]), 4),
            "avg_p_60": round(statistics.mean([r["p_60"] for r in winners]), 4),
        }
    if losers:
        results["losers"] = {
            "n": len(losers),
            "avg_p_600": round(statistics.mean([r["p_600"] for r in losers]), 4),
            "avg_p_300": round(statistics.mean([r["p_300"] for r in losers]), 4),
            "avg_p_60": round(statistics.mean([r["p_60"] for r in losers]), 4),
        }

    log(f"  N={results['n']}")
    log(f"  Avg drift 600s->300s: {results['avg_drift_600_to_300']} (abs: {results['avg_abs_drift_600_to_300']})")
    log(f"  Avg drift 300s->60s: {results['avg_drift_300_to_60']} (abs: {results['avg_abs_drift_300_to_60']})")
    log(f"  Closer to settle from 600s: {results['pct_closer_to_settle_from_600']}")
    log(f"  Closer to settle from 300s: {results['pct_closer_to_settle_from_300']}")
    if "winners" in results:
        log(f"  Winners: p_600={results['winners']['avg_p_600']} p_300={results['winners']['avg_p_300']} p_60={results['winners']['avg_p_60']}")
    if "losers" in results:
        log(f"  Losers: p_600={results['losers']['avg_p_600']} p_300={results['losers']['avg_p_300']} p_60={results['losers']['avg_p_60']}")

    return results


def explore_serve_hold(conn, events):
    """7. Serve hold rates and market implications."""
    log("\n=== 7. SERVE HOLD RATES ===")
    serve_games = []
    for e in events:
        pts = load_points_for_match(conn, e["event_ticker"])
        if len(pts) < 10:
            continue

        for i, p in enumerate(pts):
            is_game_end = False
            if i + 1 < len(pts):
                next_p = pts[i + 1]
                if (next_p["game_number"] != p["game_number"] or
                    next_p["set_number"] != p["set_number"]):
                    is_game_end = True
            else:
                is_game_end = True

            if not is_game_end or p["is_tiebreak"]:
                continue

            server = p["server"]
            holder = (p["scorer"] == server)
            serve_games.append({
                "match": e["event_ticker"],
                "set": p["set_number"],
                "game": p["game_number"],
                "server": server,
                "held": holder,
            })

    if not serve_games:
        log("  No serve games found")
        return {}

    total = len(serve_games)
    held = sum(1 for g in serve_games if g["held"])
    results = {
        "n": total,
        "hold_rate": round(held / total, 4),
        "break_rate": round(1 - held / total, 4),
    }

    # By set
    for s in [1, 2, 3]:
        sg = [g for g in serve_games if g["set"] == s]
        if sg:
            h = sum(1 for g in sg if g["held"])
            results[f"set_{s}"] = {
                "n": len(sg),
                "hold_rate": round(h / len(sg), 4),
            }
            log(f"  Set {s}: n={len(sg)} hold_rate={results[f'set_{s}']['hold_rate']}")

    # Early games vs late games
    early = [g for g in serve_games if g["game"] <= 3]
    late = [g for g in serve_games if g["game"] > 3]
    if early:
        results["early_games"] = {
            "n": len(early),
            "hold_rate": round(sum(1 for g in early if g["held"]) / len(early), 4),
        }
    if late:
        results["late_games"] = {
            "n": len(late),
            "hold_rate": round(sum(1 for g in late if g["held"]) / len(late), 4),
        }

    log(f"  Total serve games: {total}")
    log(f"  Hold rate: {results['hold_rate']}")
    log(f"  Break rate: {results['break_rate']}")
    if "early_games" in results:
        log(f"  Early games hold: {results['early_games']['hold_rate']}")
    if "late_games" in results:
        log(f"  Late games hold: {results['late_games']['hold_rate']}")

    return results


def explore_favorite_dominance(conn, events):
    """8. Favorite win rate by price threshold and time window."""
    log("\n=== 8. FAVORITE DOMINANCE ===")
    results = {}

    for w in [600, 300, 180, 120, 60]:
        for thresh in [0.50, 0.60, 0.70, 0.80, 0.85, 0.90, 0.95]:
            trades = []
            for e in events:
                p_yes = price_at(conn, e["yes_ticker"], e["close_ts"], w, 60)
                p_no = price_at(conn, e["no_ticker"], e["close_ts"], w, 60)
                if p_yes is None or p_no is None:
                    continue

                fav_price = max(p_yes, p_no)
                fav_won = (fav_price == p_yes and e["yes_result"] == "yes") or \
                          (fav_price == p_no and e["no_result"] == "yes")

                if fav_price < thresh:
                    continue

                trades.append({
                    "match": e["event_ticker"],
                    "fav_price": round(fav_price, 4),
                    "fav_won": fav_won,
                })

            if not trades:
                continue

            wins = sum(1 for t in trades if t["fav_won"])
            key = f"T-{w}s_thr{thresh:.2f}"
            results[key] = {
                "n": len(trades),
                "wins": wins,
                "hit_rate": round(wins / len(trades), 4),
                "avg_price": round(statistics.mean([t["fav_price"] for t in trades]), 4),
                "edge": round(wins / len(trades) - statistics.mean([t["fav_price"] for t in trades]), 4),
            }
            r = results[key]
            log(f"  {key}: n={r['n']} hit={r['hit_rate']} avg_price={r['avg_price']} edge={r['edge']}")

    return results


def run():
    conn = connect()
    events = load_finalized_with_both(conn)
    log(f"Finalized events with both markets: {len(events)}")

    all_results = {}
    all_results["price_trajectories"] = explore_price_trajectories(conn, events)
    all_results["set_points"] = explore_set_points(conn, events)
    all_results["break_impact"] = explore_break_impact(conn, events)
    all_results["series_differences"] = explore_series_differences(conn, events)
    all_results["set_score_transitions"] = explore_set_score_transitions(conn, events)
    all_results["price_drift"] = explore_price_drift(conn, events)
    all_results["serve_hold"] = explore_serve_hold(conn, events)
    all_results["favorite_dominance"] = explore_favorite_dominance(conn, events)

    save_json("explore_results.json", all_results)
    log("\n=== DONE. Results saved to out/explore_results.json ===")
    conn.close()
    return all_results


if __name__ == "__main__":
    run()
