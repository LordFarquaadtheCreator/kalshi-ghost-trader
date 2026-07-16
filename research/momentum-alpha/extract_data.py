#!/usr/bin/env python3
"""Extract discrete dataset from SQLite for momentum-alpha research.

Selects top N matches with best point/tick overlap.
Outputs one JSON file per match + one index file.
No repeated DB queries needed after extraction.
"""

import json
import sqlite3
import sys
from pathlib import Path

DB_PATH = "../../snapshot_for_charts.db"
OUT_DIR = Path("data/extracted")
TOP_N = 10
MIN_POINT_SPAN_SEC = 300
MIN_TICKS = 200


def find_best_matches(conn):
    """Find matches with spread points + overlapping ticks."""
    cur = conn.execute("""
        WITH good_points AS (
            SELECT match_ticker, COUNT(*) as pts,
                   MIN(ts_ms) as pt_first, MAX(ts_ms) as pt_last,
                   (MAX(ts_ms) - MIN(ts_ms))/1000 as span_sec
            FROM points
            WHERE ts_ms IS NOT NULL
            GROUP BY match_ticker
            HAVING span_sec > ?
        )
        SELECT gp.match_ticker, gp.pts, gp.span_sec,
               gp.pt_first, gp.pt_last,
               fm.home_player, fm.away_player, fm.tournament, fm.surface, fm.category,
               (SELECT COUNT(*) FROM ticks t
                JOIN markets m ON t.market_ticker = m.market_ticker
                WHERE m.event_ticker = gp.match_ticker
                AND t.ts BETWEEN gp.pt_first AND gp.pt_last) as tick_count
        FROM good_points gp
        LEFT JOIN flashscore_matches fm ON fm.event_ticker = gp.match_ticker
        GROUP BY gp.match_ticker
        ORDER BY tick_count DESC
        LIMIT ?
    """, (MIN_POINT_SPAN_SEC, TOP_N))
    cols = [d[0] for d in cur.description]
    return [dict(zip(cols, row)) for row in cur.fetchall()]


def extract_points(conn, match_ticker, pt_first, pt_last):
    """Extract all points for a match, sorted by time."""
    cur = conn.execute("""
        SELECT set_number, game_number, point_number, server, scorer,
               home_points, away_points, home_games, away_games,
               home_set_games, away_set_games, is_tiebreak, is_break_point,
               ts_ms
        FROM points
        WHERE match_ticker = ? AND ts_ms IS NOT NULL
        ORDER BY ts_ms
    """, (match_ticker,))
    cols = [d[0] for d in cur.description]
    return [dict(zip(cols, row)) for row in cur.fetchall()]


def extract_markets(conn, match_ticker):
    """Extract market metadata for a match."""
    cur = conn.execute("""
        SELECT market_ticker, player_name, tennis_competitor, status,
               occurrence_ts, open_ts, close_ts, result, settlement_ts
        FROM markets
        WHERE event_ticker = ?
    """, (match_ticker,))
    cols = [d[0] for d in cur.description]
    return [dict(zip(cols, row)) for row in cur.fetchall()]


def extract_ticks(conn, match_ticker, pt_first, pt_last):
    """Extract ticks overlapping with point timestamps."""
    cur = conn.execute("""
        SELECT t.ts, t.market_ticker, t.msg_type, t.price,
               t.yes_bid, t.yes_ask, t.yes_bid_size, t.yes_ask_size,
               t.volume, t.open_interest, t.last_trade_size,
               t.taker_side, t.taker_outcome_side, t.no_price
        FROM ticks t
        JOIN markets m ON t.market_ticker = m.market_ticker
        WHERE m.event_ticker = ?
        AND t.ts BETWEEN ? AND ?
        ORDER BY t.ts
    """, (match_ticker, pt_first, pt_last))
    cols = [d[0] for d in cur.description]
    return [dict(zip(cols, row)) for row in cur.fetchall()]


def extract_orderbook_aggregated(conn, match_ticker, pt_first, pt_last):
    """Orderbook not in snapshot DB. Return empty."""
    return []


def extract_lifecycle(conn, match_ticker, pt_first, pt_last):
    """Lifecycle not in snapshot DB. Return empty."""
    return []


def main():
    db_path = sys.argv[1] if len(sys.argv) > 1 else DB_PATH
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row

    OUT_DIR.mkdir(parents=True, exist_ok=True)

    print("Finding best matches...")
    matches = find_best_matches(conn)
    print(f"Found {len(matches)} matches")

    index = []
    for m in matches:
        ticker = m["match_ticker"]
        print(f"Extracting {ticker} ({m['home_player']} vs {m['away_player']})...")

        points = extract_points(conn, ticker, m["pt_first"], m["pt_last"])
        markets = extract_markets(conn, ticker)
        ticks = extract_ticks(conn, ticker, m["pt_first"], m["pt_last"])
        orderbook = extract_orderbook_aggregated(conn, ticker, m["pt_first"], m["pt_last"])
        lifecycle = extract_lifecycle(conn, ticker, m["pt_first"], m["pt_last"])

        data = {
            "match_ticker": ticker,
            "home_player": m["home_player"],
            "away_player": m["away_player"],
            "tournament": m["tournament"],
            "surface": m["surface"],
            "category": m["category"],
            "point_span_sec": m["span_sec"],
            "point_count": m["pts"],
            "tick_count": m["tick_count"],
            "pt_first_ts": m["pt_first"],
            "pt_last_ts": m["pt_last"],
            "markets": markets,
            "points": points,
            "ticks": ticks,
            "orderbook_1s": orderbook,
            "lifecycle": lifecycle,
        }

        outfile = OUT_DIR / f"{ticker}.json"
        with open(outfile, "w") as f:
            json.dump(data, f, indent=2, default=str)

        index.append({
            "match_ticker": ticker,
            "file": str(outfile),
            "home_player": m["home_player"],
            "away_player": m["away_player"],
            "tournament": m["tournament"],
            "point_count": m["pts"],
            "tick_count": m["tick_count"],
            "orderbook_snapshots": len(orderbook),
            "lifecycle_events": len(lifecycle),
            "span_sec": m["span_sec"],
        })
        print(f"  {len(points)} pts, {len(ticks)} ticks, {len(orderbook)} ob snapshots, {len(lifecycle)} lifecycle")

    with open(OUT_DIR / "index.json", "w") as f:
        json.dump(index, f, indent=2)

    print(f"\nDone. {len(index)} matches extracted to {OUT_DIR}/")
    conn.close()


if __name__ == "__main__":
    main()
