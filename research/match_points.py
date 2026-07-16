#!/usr/bin/env python3
"""Analyze match points and near-match points from the points table.

A match point = a point where winning it would end the match.
A near-match point = the point immediately before a match point
  (the hype buildup — e.g. 30-30 when serving for the match, or
  15-30 / 30-15 in the game that could end the match).

For each match point we record:
  - which player had it (home=1, away=2)
  - whether they converted (won the match) or saved (opponent came back)
  - the set/game/point context

Tennis rules assumed:
  - Best of 3 sets (need 2 to win)
  - Win a set: 6 games, 2-game lead, or 7-6 tiebreak
  - Win a game: 4 points, 2-point lead, or deuce/advantage
  - Tiebreak: first to 7, win by 2
"""

import sqlite3
import sys
from collections import defaultdict
from dataclasses import dataclass, field

DB_PATH = sys.argv[1] if len(sys.argv) > 1 else "kalshi_tennis.db"


@dataclass
class Point:
    set_number: int
    game_number: int
    point_number: int
    server: int        # 1=home, 2=away
    scorer: int         # 1=home won point, 2=away won point
    home_points: str    # "0","15","30","40","A"
    away_points: str
    home_games: int     # games won by home in this set at this point
    away_games: int
    home_set_games: int  # final games in completed sets before this one
    away_set_games: int
    is_tiebreak: int
    ts_ms: int          # timestamp of the point


@dataclass
class MatchPoint:
    match_ticker: str
    set_number: int
    game_number: int
    point_number: int
    player: int          # who has the match point (1=home, 2=away)
    converted: bool      # did they win the match from here
    ts_ms: int
    context: str         # e.g. "40-30", "6-5 serving", "tiebreak 6-5"
    server: int          # who is serving this point (1=home, 2=away)
    is_tiebreak: bool    # is this a tiebreak match point


@dataclass
class MatchResult:
    match_ticker: str
    winner: int           # 1=home, 2=away
    total_sets_home: int
    total_sets_away: int
    match_points: list[MatchPoint] = field(default_factory=list)
    near_match_points: list[MatchPoint] = field(default_factory=list)


def point_value(p: str) -> int:
    """Convert tennis point string to numeric value for comparison."""
    if p == "0": return 0
    if p == "15": return 1
    if p == "30": return 2
    if p == "40": return 3
    if p == "A": return 4  # advantage
    return 0


def sets_won_before_this_set(home_set_games: int, away_set_games: int) -> tuple[int, int]:
    """home_set_games/away_set_games contain completed set scores.
    Count how many sets each player has won."""
    # This field is a bit ambiguous — it might be cumulative games or set count.
    # Actually from the schema: "final games in completed sets before this one"
    # This is a single number, not a list. So it's likely total games across
    # completed sets, not set count. We need to infer sets won differently.
    # We'll determine sets won by looking at the data pattern.
    return (0, 0)  # placeholder — we'll compute differently


def analyze_match(match_ticker: str, points: list[Point]) -> MatchResult | None:
    """Replay a match and find all match points."""
    if not points:
        return None

    # Sort points by set, game, point number
    points.sort(key=lambda p: (p.set_number, p.game_number, p.point_number))

    # Determine match winner: who won the last point of the last set
    last = points[-1]
    max_set = max(p.set_number for p in points)

    # Count sets won by each player by looking at set transitions
    # A set is won when we move to the next set_number
    sets_home = 0
    sets_away = 0

    # Track games within each set
    set_games: dict[int, dict[int, int]] = defaultdict(lambda: {1: 0, 2: 0})

    for p in points:
        # When set number increases, the previous set's winner is determined
        # by looking at home_games/away_games of the last point in that set
        pass

    # Better approach: find the last point of each set, determine winner
    set_last_points: dict[int, Point] = {}
    for p in points:
        set_last_points[p.set_number] = p

    for set_num in sorted(set_last_points.keys()):
        p = set_last_points[set_num]
        # Winner of the set = who has more games
        if p.home_games > p.away_games:
            sets_home += 1
        elif p.away_games > p.home_games:
            sets_away += 1
        # tiebreak: check who won the last point
        elif p.is_tiebreak:
            if p.scorer == 1:
                sets_home += 1
            else:
                sets_away += 1

    winner = 1 if sets_home > sets_away else 2
    result = MatchResult(match_ticker, winner, sets_home, sets_away)

    # Now replay to find match points
    # A match point occurs when:
    # 1. The player can win the current point to win the current game
    # 2. Winning that game would win the current set
    # 3. Winning that set would win the match

    sets_won_so_far = {1: 0, 2: 0}
    sets_needed = 2  # best of 3

    # Process set by set
    for set_num in sorted(set_last_points.keys()):
        set_points = [p for p in points if p.set_number == set_num]
        if not set_points:
            continue

        is_final_set = (sets_won_so_far[1] == 1 and sets_won_so_far[2] == 1)
        # If not final set, winning this set doesn't end the match
        # unless one player has 0 sets and this would be their 2nd... no
        # Match point only when winning the set wins the match
        # That means: player already has sets_needed - 1 sets won

        for i, p in enumerate(set_points):
            # How many sets would each player have if they won this set?
            # We need to know if this point could win the game, and if winning
            # the game could win the set

            # Current game score
            hp = point_value(p.home_points)
            ap = point_value(p.away_points)

            # Who is about to win the current point?
            # A point is a "game point" if one player is at 40 or A and the other is below
            # In tiebreak: game point = point that wins the tiebreak

            if p.is_tiebreak:
                # Tiebreak: first to 7, win by 2
                # Points in tiebreak are sequential — point_number within the game
                tb_home = 0
                tb_away = 0
                for j, tp in enumerate(set_points):
                    if tp.game_number != p.game_number:
                        continue
                    if tp.point_number < p.point_number:
                        if tp.scorer == 1:
                            tb_home += 1
                        else:
                            tb_away += 1

                # Current point's score IS the tiebreak score
                # home_points/away_points in tiebreak might be the TB score
                # Let's use the cumulative count up to and including this point
                # Actually, let's count points won in this tiebreak up to this point
                tb_score_home = 0
                tb_score_away = 0
                for tp in set_points:
                    if tp.game_number == p.game_number and tp.point_number <= p.point_number:
                        if tp.scorer == 1:
                            tb_score_home += 1
                        else:
                            tb_score_away += 1

                # Before this point was played, the score was:
                pre_tb_home = tb_score_home - (1 if p.scorer == 1 else 0)
                pre_tb_away = tb_score_away - (1 if p.scorer == 2 else 0)

                # Match point in tiebreak: winning this point wins TB, which wins set, which wins match
                for player in [1, 2]:
                    opp = 2 if player == 1 else 1
                    pre_self = pre_tb_home if player == 1 else pre_tb_away
                    pre_opp = pre_tb_away if player == 1 else pre_tb_home
                    # Can winning this point win the tiebreak?
                    if pre_self + 1 >= 7 and pre_self + 1 - pre_opp >= 2:
                        # Winning this point wins the tiebreak → wins the set
                        # Does winning this set win the match?
                        if sets_won_so_far[player] + 1 >= sets_needed:
                            # This is a match point!
                            converted = (p.scorer == player)
                            context = f"TB {pre_tb_home}-{pre_tb_away}"
                            mp = MatchPoint(match_ticker, p.set_number, p.game_number,
                                          p.point_number, player, converted, p.ts_ms, context,
                                          p.server, True)
                            result.match_points.append(mp)

                            # Near match point: the point before this
                            if i > 0:
                                prev = set_points[i - 1]
                                if prev.game_number == p.game_number:  # same game/tiebreak
                                    pre_prev_home = pre_tb_home - (1 if prev.scorer == 1 else 0)
                                    pre_prev_away = pre_tb_away - (1 if prev.scorer == 2 else 0)
                                    nmp = MatchPoint(match_ticker, prev.set_number, prev.game_number,
                                                   prev.point_number, player, False, prev.ts_ms,
                                                   f"TB {pre_prev_home}-{pre_prev_away} (pre-MP)",
                                                   prev.server, True)
                                    result.near_match_points.append(nmp)
            else:
                # Regular game
                # Game point: one player at 40 (3) or A (4), other below
                for player in [1, 2]:
                    opp = 2 if player == 1 else 2
                    self_score = hp if player == 1 else ap
                    opp_score = ap if player == 1 else hp

                    # Can this player win the game by winning this point?
                    if self_score >= 3 and self_score > opp_score:
                        # Winning this point wins the game
                        # Would winning this game win the set?
                        curr_games_home = p.home_games
                        curr_games_away = p.away_games

                        # home_games/away_games = games won at this point in the set
                        # If player wins this game, their games increase by 1
                        if player == 1:
                            new_games = curr_games_home + 1
                            opp_games = curr_games_away
                        else:
                            new_games = curr_games_away + 1
                            opp_games = curr_games_home

                        # Does winning this game win the set?
                        wins_set = False
                        if new_games >= 6 and new_games - opp_games >= 2:
                            wins_set = True
                        elif new_games == 7 and opp_games == 5:
                            wins_set = True
                        elif new_games == 7 and opp_games == 6:
                            # 7-6 — but this would be a tiebreak, handled above
                            wins_set = True

                        if wins_set:
                            # Does winning this set win the match?
                            if sets_won_so_far[player] + 1 >= sets_needed:
                                # Match point!
                                converted = (p.scorer == player)
                                context = f"{'40' if self_score == 3 else 'A'}-{'40' if opp_score == 3 else '30' if opp_score == 2 else '15' if opp_score == 1 else '0'}"
                                mp = MatchPoint(match_ticker, p.set_number, p.game_number,
                                              p.point_number, player, converted, p.ts_ms, context,
                                              p.server, False)
                                result.match_points.append(mp)

                                # Near match point: previous point in same game
                                if i > 0:
                                    prev = set_points[i - 1]
                                    if prev.game_number == p.game_number and not prev.is_tiebreak:
                                        prev_hp = point_value(prev.home_points)
                                        prev_ap = point_value(prev.away_points)
                                        prev_self = prev_hp if player == 1 else prev_ap
                                        prev_opp = prev_ap if player == 1 else prev_hp
                                        prev_context = f"{'40' if prev_self == 3 else '30' if prev_self == 2 else '15' if prev_self == 1 else '0'}-{'40' if prev_opp == 3 else '30' if prev_opp == 2 else '15' if prev_opp == 1 else '0'}"
                                        nmp = MatchPoint(match_ticker, prev.set_number, prev.game_number,
                                                       prev.point_number, player, False, prev.ts_ms,
                                                       f"{prev_context} (pre-MP)",
                                                       prev.server, False)
                                        result.near_match_points.append(nmp)

        # Update sets won after processing this set
        p = set_last_points[set_num]
        if p.home_games > p.away_games:
            sets_won_so_far[1] += 1
        elif p.away_games > p.home_games:
            sets_won_so_far[2] += 1
        elif p.is_tiebreak:
            if p.scorer == 1:
                sets_won_so_far[1] += 1
            else:
                sets_won_so_far[2] += 1

    return result


def main():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all matches with points
    cur.execute("SELECT DISTINCT match_ticker FROM points ORDER BY match_ticker")
    match_tickers = [r[0] for r in cur.fetchall()]
    print(f"Matches with point data: {len(match_tickers)}")

    all_results = []
    for mt in match_tickers:
        cur.execute("""
            SELECT set_number, game_number, point_number, server, scorer,
                   home_points, away_points, home_games, away_games,
                   home_set_games, away_set_games, is_tiebreak, ts_ms
            FROM points WHERE match_ticker = ?
            ORDER BY set_number, game_number, point_number
        """, (mt,))
        rows = cur.fetchall()
        points = [Point(
            set_number=r[0], game_number=r[1], point_number=r[2],
            server=r[3], scorer=r[4], home_points=r[5], away_points=r[6],
            home_games=r[7], away_games=r[8], home_set_games=r[9],
            away_set_games=r[10], is_tiebreak=r[11], ts_ms=r[12] or 0
        ) for r in rows]
        result = analyze_match(mt, points)
        if result:
            all_results.append(result)

    print(f"Matches analyzed: {len(all_results)}")

    # Aggregate match point stats
    total_mp = sum(len(r.match_points) for r in all_results)
    converted_mp = sum(1 for r in all_results for mp in r.match_points if mp.converted)
    saved_mp = total_mp - converted_mp

    total_nmp = sum(len(r.near_match_points) for r in all_results)

    print(f"\n=== MATCH POINTS ===")
    print(f"Total match points found: {total_mp}")
    print(f"Converted (won match): {converted_mp} ({converted_mp/total_mp*100:.1f}%)" if total_mp else "")
    print(f"Saved (opponent came back): {saved_mp} ({saved_mp/total_mp*100:.1f}%)" if total_mp else "")

    print(f"\n=== NEAR MATCH POINTS ===")
    print(f"Total near-match points: {total_nmp}")

    # Per-match breakdown: matches with 1 MP vs multiple MPs
    matches_with_mp = [r for r in all_results if r.match_points]
    matches_one_mp = sum(1 for r in matches_with_mp if len(r.match_points) == 1)
    matches_multi_mp = sum(1 for r in matches_with_mp if len(r.match_points) > 1)
    print(f"\n=== PER MATCH ===")
    print(f"Matches with at least 1 MP: {len(matches_with_mp)}")
    print(f"Matches with exactly 1 MP: {matches_one_mp}")
    print(f"Matches with multiple MPs: {matches_multi_mp}")

    # Conversion rate by set
    print(f"\n=== BY SET ===")
    by_set = defaultdict(lambda: {"total": 0, "converted": 0})
    for r in all_results:
        for mp in r.match_points:
            by_set[mp.set_number]["total"] += 1
            if mp.converted:
                by_set[mp.set_number]["converted"] += 1
    for s in sorted(by_set.keys()):
        d = by_set[s]
        pct = d["converted"] / d["total"] * 100 if d["total"] else 0
        print(f"  Set {s}: {d['converted']}/{d['total']} converted ({pct:.1f}%)")

    # Tiebreak vs non-tiebreak
    tb_mp = sum(1 for r in all_results for mp in r.match_points if "TB" in mp.context)
    tb_conv = sum(1 for r in all_results for mp in r.match_points if mp.converted and "TB" in mp.context)
    non_tb_mp = total_mp - tb_mp
    non_tb_conv = converted_mp - tb_conv
    print(f"\n=== TIEBREAK vs REGULAR ===")
    if tb_mp:
        print(f"  Tiebreak MPs: {tb_conv}/{tb_mp} ({tb_conv/tb_mp*100:.1f}%)")
    if non_tb_mp:
        print(f"  Regular MPs: {non_tb_conv}/{non_tb_mp} ({non_tb_conv/non_tb_mp*100:.1f}%)")

    # Deep set breakdown
    print(f"\n=== SET 2 vs SET 3 DEEP BREAKDOWN ===")
    for set_num in [2, 3]:
        mps = [mp for r in all_results for mp in r.match_points if mp.set_number == set_num]
        if not mps:
            continue
        total = len(mps)
        conv = sum(1 for mp in mps if mp.converted)
        saved = total - conv
        tb = [mp for mp in mps if mp.is_tiebreak]
        tb_conv = sum(1 for mp in tb if mp.converted)
        reg = [mp for mp in mps if not mp.is_tiebreak]
        reg_conv = sum(1 for mp in reg if mp.converted)
        serve = [mp for mp in mps if mp.server == mp.player]
        serve_conv = sum(1 for mp in serve if mp.converted)
        ret = [mp for mp in mps if mp.server != mp.player]
        ret_conv = sum(1 for mp in ret if mp.converted)

        print(f"\n  --- SET {set_num} ---")
        print(f"  Total MPs: {total}, Converted: {conv} ({conv/total*100:.1f}%), Saved: {saved} ({saved/total*100:.1f}%)")
        if reg:
            print(f"  Regular: {reg_conv}/{len(reg)} ({reg_conv/len(reg)*100:.1f}%)")
        if tb:
            print(f"  Tiebreak: {tb_conv}/{len(tb)} ({tb_conv/len(tb)*100:.1f}%)")
        if serve:
            print(f"  Serving for match: {serve_conv}/{len(serve)} ({serve_conv/len(serve)*100:.1f}%)")
        if ret:
            print(f"  Returning for match: {ret_conv}/{len(ret)} ({ret_conv/len(ret)*100:.1f}%)")

        # Context breakdown (non-tiebreak)
        by_context = defaultdict(lambda: {"total": 0, "converted": 0})
        for mp in reg:
            by_context[mp.context]["total"] += 1
            if mp.converted:
                by_context[mp.context]["converted"] += 1
        print(f"  Regular MP contexts:")
        for ctx in sorted(by_context.keys(), key=lambda c: by_context[c]["total"], reverse=True):
            d = by_context[ctx]
            pct = d["converted"] / d["total"] * 100 if d["total"] else 0
            print(f"    {ctx}: {d['converted']}/{d['total']} ({pct:.1f}%)")

    # Detail: matches where MP was saved but player still won
    print(f"\n=== SAVED BUT STILL WON ===")
    saved_but_won = 0
    saved_and_lost = 0
    for r in all_results:
        for mp in r.match_points:
            if not mp.converted:
                if mp.player == r.winner:
                    saved_but_won += 1
                else:
                    saved_and_lost += 1
    print(f"  Had MP saved, still won match: {saved_but_won}")
    print(f"  Had MP saved, lost match: {saved_and_lost}")

    conn.close()


if __name__ == "__main__":
    main()
