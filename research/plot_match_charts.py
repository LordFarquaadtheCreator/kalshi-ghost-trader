#!/usr/bin/env python3
"""
Generate XY scatter charts for Kalshi tennis matches.
"""

import sqlite3
import os
import sys
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime, timezone
import numpy as np

DB = os.path.expanduser("~/kalshi-ghost-trader/snapshot_for_charts.db")
OUTPUT_DIR = os.path.expanduser("~/kalshi-ghost-trader/research/charts")
os.makedirs(OUTPUT_DIR, exist_ok=True)

def log(msg):
    print(msg, flush=True)
def get_candidate_matches(conn):
    """Get all matches that have streamed points + both market tick data."""
    query = """
    SELECT 
        p.match_ticker,
        e.title,
        e.series_ticker,
        (SELECT GROUP_CONCAT(games, ' ') FROM (
            SELECT p2.match_ticker, p2.set_number, 
                   MAX(p2.home_games) || '-' || MAX(p2.away_games) as games
            FROM points p2 WHERE p2.match_ticker = p.match_ticker
            GROUP BY p2.match_ticker, p2.set_number
            ORDER BY p2.set_number
        )) as score_line,
        fs.home_player,
        fs.away_player
    FROM points p
    JOIN events e ON p.match_ticker = e.event_ticker
    LEFT JOIN flashscore_matches fs ON p.match_ticker = fs.event_ticker
    GROUP BY p.match_ticker
    HAVING COUNT(DISTINCT p.ts_ms) > 1
       AND (SELECT COUNT(DISTINCT market_ticker) FROM ticks WHERE market_ticker LIKE p.match_ticker || '%') >= 2
    ORDER BY e.title
    """
    return conn.execute(query).fetchall()

def get_market_info(conn, event_ticker):
    query = """
    SELECT market_ticker, player_name, result
    FROM markets
    WHERE event_ticker = ?
    """
    rows = conn.execute(query, (event_ticker,)).fetchall()
    info = {}
    for row in rows:
        info[row[1]] = {'ticker': row[0], 'result': row[2]}
    return info

def get_tick_data(conn, match_ticker):
    query = """
    SELECT t.market_ticker, t.ts, t.price
    FROM ticks t
    WHERE t.market_ticker LIKE ? || '%'
      AND t.price IS NOT NULL
      AND t.ts IS NOT NULL
    ORDER BY t.ts
    """
    return conn.execute(query, (match_ticker + '%',)).fetchall()

def get_point_data(conn, match_ticker):
    query = """
    SELECT set_number, game_number, point_number,
           home_points, away_points, home_games, away_games,
           is_tiebreak, ts_ms
    FROM points
    WHERE match_ticker = ?
      AND ts_ms IS NOT NULL
    ORDER BY ts_ms, set_number, game_number, point_number
    """
    return conn.execute(query, (match_ticker,)).fetchall()

def build_score_events(pts):
    """Build list of (time_unix_s, score_text) for score changes."""
    events = []
    
    if not pts:
        return events
    
    ts_ms_0 = pts[0][8]
    events.append((ts_ms_0 / 1000.0, "0-0"))
    
    prev_set = 1
    prev_hg = 0
    prev_ag = 0
    prev_g = 0
    
    home_set_wins = 0
    away_set_wins = 0
    completed_sets = []  # list of "X-Y" strings
    last_recorded_score = "0-0"
    
    for pt in pts:
        ts_s = pt[8] / 1000.0
        if ts_s == 0:
            continue
        
        s = pt[0]  # set_number
        g = pt[1]  # game_number
        hg = pt[5]  # home_games (games won in current set by home)
        ag = pt[6]  # away_games
        
        # Detect set change (set number increased)
        if s > prev_set:
            # Record the completed set score using prev_hg/prev_ag
            completed_sets.append(f"{prev_hg}-{prev_ag}")
            # Update set wins
            if prev_hg > prev_ag:
                home_set_wins += 1
            else:
                away_set_wins += 1
            prev_set = s
            prev_hg = 0
            prev_ag = 0
            prev_g = 0
            
            # Build current overall score
            current_set = f"0-0"
            if completed_sets:
                overall = " | ".join(completed_sets + [current_set])
            else:
                overall = current_set
            score_text = f"Set {s}: {home_set_wins}-{away_set_wins} [{overall}]"
            if score_text != last_recorded_score:
                events.append((ts_s, score_text))
                last_recorded_score = score_text
        
        # Detect game change within same set
        elif g != prev_g and g > 1:
            # A game was won — update prev_hg/prev_ag
            # At this point hg/ag reflect games after the game was won
            prev_hg = hg
            prev_ag = ag
            
            current_set = f"{hg}-{ag}"
            if completed_sets:
                overall = " | ".join(completed_sets + [current_set])
            else:
                overall = current_set
            if overall != last_recorded_score:
                events.append((ts_s, overall))
                last_recorded_score = overall
        
        prev_g = g
    
    # Final set score (if we ended mid-set)
    if prev_hg > 0 or prev_ag > 0:
        current_set = f"{prev_hg}-{prev_ag}"
        if completed_sets:
            overall = " | ".join(completed_sets + [current_set])
        else:
            overall = current_set
        # Use last point timestamp
        last_ts = pts[-1][8] / 1000.0
        if overall != last_recorded_score and last_ts > 0:
            events.append((last_ts, overall))
    
    return events

def plot_match(match, conn):
    event_ticker, title, series_ticker, score_line, fs_home, fs_away = match[:6]
    log(f"Plotting: {title} ({score_line})")
    
    market_info = get_market_info(conn, event_ticker)
    all_players = list(market_info.keys())
    if len(all_players) < 2:
        log(f"  SKIP: < 2 players")
        return
    
    player_a, player_b = all_players[0], all_players[1]
    player_a_mkt = market_info[player_a]['ticker']
    player_b_mkt = market_info[player_b]['ticker']
    # Get tick data - downsample to max 500 points for performance
    all_ticks = get_tick_data(conn, event_ticker)
    log(f"  Ticks: {len(all_ticks)} total")
    
    ticks_a = [(t[1]/1000.0, t[2]) for t in all_ticks if t[0] == player_a_mkt and t[2] is not None]
    ticks_b = [(t[1]/1000.0, t[2]) for t in all_ticks if t[0] == player_b_mkt and t[2] is not None]
    
    # Downsample for performance
    max_ticks = 500
    if len(ticks_a) > max_ticks:
        step = len(ticks_a) // max_ticks
        ticks_a = ticks_a[::step]
    if len(ticks_b) > max_ticks:
        step = len(ticks_b) // max_ticks
        ticks_b = ticks_b[::step]
    log(f"  Ticks A ({player_a}): {len(ticks_a)} (downsampled), B ({player_b}): {len(ticks_b)} (downsampled)")
    
    if len(ticks_a) < 5 or len(ticks_b) < 5:
        log(f"  SKIP: too few ticks")
        return
    
    pts = get_point_data(conn, event_ticker)
    log(f"  Points: {len(pts)}")
    
    score_events = build_score_events(pts)
    log(f"  Score events: {len(score_events)}")
    
    if not score_events:
        log(f"  SKIP: no score events")
        return
    
    # Crop x-axis to match window: from first point to last point + 1 hour buffer
    pt_times = [p[8]/1000.0 for p in pts if p[8] and p[8] > 0]
    match_start = min(pt_times) - 3600  # 1 hour before first point
    match_end = max(pt_times) + 3600    # 1 hour after last point
    
    # Filter ticks to match window
    ticks_a = [t for t in ticks_a if match_start <= t[0] <= match_end]
    ticks_b = [t for t in ticks_b if match_start <= t[0] <= match_end]
    
    # Time range from filtered ticks
    all_times = [t[0] for t in ticks_a + ticks_b]
    if not all_times:
        log(f"  SKIP: no ticks in match window")
        return
    t_min = min(all_times)
    t_max = max(all_times)
    
    def to_dn(ts):
        return mdates.date2num(datetime.fromtimestamp(ts, tz=timezone.utc))
    
    ta_dn = [to_dn(t[0]) for t in ticks_a]
    ta_p = [t[1] for t in ticks_a]
    tb_dn = [to_dn(t[0]) for t in ticks_b]
    tb_p = [t[1] for t in ticks_b]
    
    # Filter score events to tick window
    se = [(ts, txt) for ts, txt in score_events if t_min <= ts <= t_max]
    
    # Deduplicate
    seen = set()
    se_unique = []
    for ts, txt in se:
        if txt not in seen:
            seen.add(txt)
            se_unique.append((ts, txt))
    
    log(f"  Unique score events in window: {len(se_unique)}")
    
    if not se_unique:
        log(f"  SKIP: no unique score events in window")
        return
    
    fig, ax1 = plt.subplots(figsize=(14, 7))
    
    color_a = '#2196F3'
    color_b = '#FF5722'
    
    ax1.plot(ta_dn, ta_p, '-', color=color_a, linewidth=1.2, alpha=0.9, label=player_a, zorder=3)
    ax1.plot(tb_dn, tb_p, '-', color=color_b, linewidth=1.2, alpha=0.9, label=player_b, zorder=3)
    ax1.scatter(ta_dn, ta_p, s=3, color=color_a, alpha=0.3, zorder=2)
    ax1.scatter(tb_dn, tb_p, s=3, color=color_b, alpha=0.3, zorder=2)
    
    mkt_a_res = market_info[player_a]['result']
    mkt_b_res = market_info[player_b]['result']
    
    ax1.scatter(ta_dn[-1], ta_p[-1], s=80, color=color_a, marker='o', edgecolors='black', linewidth=1.5, zorder=5)
    ax1.scatter(tb_dn[-1], tb_p[-1], s=80, color=color_b, marker='o', edgecolors='black', linewidth=1.5, zorder=5)
    
    ax1.axhline(y=1.0, color='green', linestyle='--', linewidth=0.8, alpha=0.4)
    ax1.axhline(y=0.0, color='red', linestyle='--', linewidth=0.8, alpha=0.4)
    ax1.axhline(y=0.5, color='gray', linestyle=':', linewidth=0.6, alpha=0.3)
    
    # Top axis for score annotations
    event_times_dn = [to_dn(ts) for ts, _ in se_unique]
    event_texts = [txt for _, txt in se_unique]
    
    ax_top = ax1.secondary_xaxis(location='top')
    ax_top.set_xticks(event_times_dn)
    ax_top.set_xticklabels(event_texts, fontsize=6.5, rotation=45, ha='left', va='bottom')
    ax_top.tick_params(axis='x', length=4, pad=2)
    ax_top.set_xlim(ax1.get_xlim())
    
    # Vertical lines for set boundaries
    for ts, txt in se_unique:
        if 'Set' in txt or ('|' in txt and len(txt) > 6):
            dn = to_dn(ts)
            ax1.axvline(x=dn, color='gray', linestyle=':', linewidth=0.5, alpha=0.3)
    
    ax1.set_ylim(-0.05, 1.15)
    ax1.set_ylabel('Price', fontsize=11)
    ax1.set_xlabel('Time (UTC)', fontsize=11)
    
    winner = player_a if mkt_a_res == 'yes' else player_b
    ax1.set_title(f"{title}  |  Final: {score_line}  |  W: {winner}", fontsize=12, fontweight='bold')
    
    locator = mdates.AutoDateLocator(minticks=3, maxticks=8)
    formatter = mdates.DateFormatter('%H:%M\n%b %d', tz=timezone.utc)
    ax1.xaxis.set_major_locator(locator)
    ax1.xaxis.set_major_formatter(formatter)
    
    ax1.legend([
        f"{player_a}  [{mkt_a_res.upper()}]",
        f"{player_b}  [{mkt_b_res.upper()}]"
    ], loc='upper left', fontsize=9)
    
    ax1.grid(True, alpha=0.3)
    fig.tight_layout()
    
    safe_title = title.replace(' ', '_').replace('/', '_').replace(',', '').replace("'", '')
    outpath = os.path.join(OUTPUT_DIR, f"{safe_title}.png")
    fig.savefig(outpath, dpi=150, bbox_inches='tight')
    plt.close(fig)
    log(f"  Saved: {outpath}")


def main():
    conn = sqlite3.connect(DB)
    matches = get_candidate_matches(conn)
    log(f"Found {len(matches)} candidate matches")
    
    for i, match in enumerate(matches):
        print(f"[{i+1}/{len(matches)}]", flush=True, end=' ')
        try:
            plot_match(match, conn)
        except Exception as e:
            log(f"  ERROR: {e}")
            import traceback
            traceback.print_exc()
    
    conn.close()
    log(f"\nDone! Charts in {OUTPUT_DIR}")

if __name__ == '__main__':
    main()
