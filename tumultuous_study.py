#!/usr/bin/env python3
"""
Analyze which settled matches had "tumultuous" contract price action.
Tumultuous = large price swings (e.g. 90c -> 20c -> 99c -> 1c)
"""
import sqlite3, os, sys, json
from collections import defaultdict

DB = os.path.expanduser("~/kalshi-ghost-trader/snapshot_for_charts.db")
OUTPUT = os.path.expanduser("~/kalshi-ghost-trader/tumultuous_study.json")

def get_matches(conn):
    """Get all matches with points + both market ticks."""
    cur = conn.execute("""
        SELECT p.match_ticker, e.title, e.series_ticker,
               (SELECT GROUP_CONCAT(games, ' ') FROM (
                   SELECT p2.match_ticker, p2.set_number, 
                          MAX(p2.home_games) || '-' || MAX(p2.away_games) as games
                   FROM points p2 WHERE p2.match_ticker = p.match_ticker
                   GROUP BY p2.match_ticker, p2.set_number
                   ORDER BY p2.set_number
               )) as score_line,
               fs.home_player, fs.away_player
        FROM points p
        JOIN events e ON p.match_ticker = e.event_ticker
        LEFT JOIN flashscore_matches fs ON p.match_ticker = fs.event_ticker
        GROUP BY p.match_ticker
        HAVING COUNT(DISTINCT p.ts_ms) > 1
           AND (SELECT COUNT(DISTINCT market_ticker) FROM ticks WHERE market_ticker LIKE p.match_ticker || '%') >= 2
        ORDER BY e.title
    """)
    return cur.fetchall()

def get_market_info(conn, event_ticker):
    cur = conn.execute("SELECT market_ticker, player_name, result FROM markets WHERE event_ticker = ?", (event_ticker,))
    info = {}
    for row in cur.fetchall():
        info[row[1]] = {'ticker': row[0], 'result': row[2]}
    return info

def compute_volatility(prices):
    """Compute volatility metrics from a list of (time_s, price) tuples."""
    if len(prices) < 10:
        return None
    
    # Sort by time
    prices = sorted(prices, key=lambda x: x[0])
    
    vals = [p[1] for p in prices]
    n = len(vals)
    
    # Basic stats
    min_p = min(vals)
    max_p = max(vals)
    range_p = max_p - min_p
    avg_p = sum(vals) / n
    
    # Standard deviation
    variance = sum((v - avg_p) ** 2 for v in vals) / n
    std_dev = variance ** 0.5
    
    # Cumulative absolute change (total path length)
    cum_abs_change = sum(abs(vals[i] - vals[i-1]) for i in range(1, n))
    
    # Number of directional changes (local extrema)
    directions = []
    for i in range(1, n):
        diff = vals[i] - vals[i-1]
        if diff > 0.001:
            directions.append(1)
        elif diff < -0.001:
            directions.append(-1)
        else:
            directions.append(0)
    
    reversals = 0
    for i in range(1, len(directions)):
        if directions[i] != 0 and directions[i-1] != 0 and directions[i] != directions[i-1]:
            reversals += 1
    
    # How many times does price cross major thresholds?
    # Favorite threshold: 0.5
    above_50 = sum(1 for v in vals if v > 0.50)
    below_50 = sum(1 for v in vals if v < 0.50)
    
    # Extreme crosses: went >0.80 *and* <0.20 at some point
    went_above_80 = any(v > 0.80 for v in vals)
    went_below_20 = any(v < 0.20 for v in vals)
    
    # Number of times price crossed 0.5 (changed favorite)
    # Count transitions across 0.5
    cross_50 = 0
    for i in range(1, len(vals)):
        if (vals[i-1] - 0.5) * (vals[i] - 0.5) < 0:
            cross_50 += 1
    
    # Volatility score (composite)
    # Higher = more tumultuous
    volatility_score = (
        + range_p * 3.0           # max swing
        + std_dev * 5.0           # standard deviation
        + (cum_abs_change / max(1, n)) * 10.0  # average step size
        + reversals * 0.5         # number of reversals
        + cross_50 * 2.0          # times favorite flipped
        + (3.0 if went_above_80 and went_below_20 else 0.0)  # extreme swing bonus
    )
    
    return {
        'n_ticks': n,
        'min_price': round(min_p, 3),
        'max_price': round(max_p, 3),
        'price_range': round(range_p, 3),
        'avg_price': round(avg_p, 3),
        'std_dev': round(std_dev, 3),
        'cum_abs_change': round(cum_abs_change, 2),
        'reversals': reversals,
        'crossed_50': cross_50,
        'went_above_80': went_above_80,
        'went_below_20': went_below_20,
        'volatility_score': round(volatility_score, 2),
    }

def main():
    conn = sqlite3.connect(DB)
    matches = get_matches(conn)
    
    results = []
    
    for match in matches:
        event_ticker, title, series, score_line = match[:4]
        
        market_info = get_market_info(conn, event_ticker)
        players = list(market_info.keys())
        if len(players) < 2:
            continue
        
        p_a, p_b = players[0], players[1]
        t_a = market_info[p_a]['ticker']
        t_b = market_info[p_b]['ticker']
        res_a = market_info[p_a]['result']
        winner = p_a if res_a == 'yes' else p_b
        loser = p_b if res_a == 'yes' else p_a
        
        # Get ticks, crop to match window
        all_ticks = conn.execute("SELECT market_ticker, ts, price FROM ticks WHERE market_ticker LIKE ? || '%' AND price IS NOT NULL AND ts IS NOT NULL ORDER BY ts", (event_ticker + '%',)).fetchall()
        
        ticks_a = [(t[1]/1000.0, t[2]) for t in all_ticks if t[0] == t_a]
        ticks_b = [(t[1]/1000.0, t[2]) for t in all_ticks if t[0] == t_b]
        
        # Get point time range to crop
        pt_rows = conn.execute("SELECT ts_ms FROM points WHERE match_ticker = ? AND ts_ms IS NOT NULL AND ts_ms > 0 ORDER BY ts_ms", (event_ticker,)).fetchall()
        if not pt_rows:
            continue
        pt_times = [r[0]/1000.0 for r in pt_rows]
        match_start = min(pt_times) - 3600
        match_end = max(pt_times) + 3600
        
        ticks_a = [t for t in ticks_a if match_start <= t[0] <= match_end]
        ticks_b = [t for t in ticks_b if match_start <= t[0] <= match_end]
        
        if len(ticks_a) < 10 or len(ticks_b) < 10:
            continue
        
        vol_a = compute_volatility(ticks_a)
        vol_b = compute_volatility(ticks_b)
        
        if not vol_a or not vol_b:
            continue
        
        # The "interesting" volatility is from the winner's price swings
        winner_vol = vol_a if res_a == 'yes' else vol_b
        loser_vol = vol_b if res_a == 'yes' else vol_a
        
        # Also compute a combined score
        combined_score = winner_vol['volatility_score'] + loser_vol['volatility_score']
        
        # Compute point data duration
        pt_duration_mins = round((max(pt_times) - min(pt_times)) / 60, 1)
        
        # Determine if score looks complete
        last_set_str = score_line.split()[-1] if score_line else ''
        # A complete set has 6+ games or 7 in tiebreak
        has_complete_score = False
        if '-' in last_set_str:
            parts = last_set_str.split('-')
            if len(parts) == 2:
                try:
                    h = int(parts[0])
                    a = int(parts[1])
                    if h >= 6 or a >= 6 or h >= 7 or a >= 7:
                        has_complete_score = True
                except:
                    pass
        
        results.append({
            'event_ticker': event_ticker,
            'title': title,
            'series': series,
            'score_line': score_line,
            'has_complete_score': has_complete_score,
            'point_duration_mins': pt_duration_mins,
            'winner': winner,
            'loser': loser,
            'winner_volatility': winner_vol,
            'loser_volatility': loser_vol,
            'combined_volatility_score': round(combined_score, 2),
        })
    
    # Sort by combined volatility score descending
    results.sort(key=lambda r: r['combined_volatility_score'], reverse=True)
    
    print(f"=== TUMULTUOUS MATCH STUDY ===\n")
    print(f"Analyzed {len(results)} matches with tick + point data\n")
    
    print(f"{'Rank':<5} {'Match':<32} {'Score':<18} {'Sets':<5} {'Vol Score':<10} {'Winner Range':<14} {'Loser Range':<14} {'Reversals':<10} {'X>80&<20':<10} {'X50':<5}")
    print("-" * 130)
    
    for i, r in enumerate(results, 1):
        wv = r['winner_volatility']
        lv = r['loser_volatility']
        
        # Determine set count from score line
        set_count = len(r['score_line'].split()) if r['score_line'] else 0
        
        wr = f"{wv['min_price']}-{wv['max_price']}"
        lr = f"{lv['min_price']}-{lv['max_price']}"
        
        extreme = "✓" if (wv['went_above_80'] and wv['went_below_20']) or (lv['went_above_80'] and lv['went_below_20']) else ""
        
        # Mark truly tumultuous
        tumult_mark = "★" if (wv['went_above_80'] and wv['went_below_20']) else ""
        
        title_short = r['title'][:30]
        score_short = r['score_line'][:16]
        
        print(f"{tumult_mark}{i:<4} {title_short:<32} {score_short:<18} {set_count:<5} {r['combined_volatility_score']:<10} {wr:<14} {lr:<14} {wv['reversals']:<10} {extreme:<10} {wv['crossed_50']:<5}")
    
    print(f"\n\n=== HIGHLY TUMULTUOUS (★ = winner >80c AND <20c) ===")
    for r in results:
        wv = r['winner_volatility']
        if wv['went_above_80'] and wv['went_below_20']:
            print(f"\n{r['title']} ({r['score_line']})")
            print(f"  Winner: {r['winner']} | Price range: {wv['min_price']}c - {wv['max_price']}c")
            print(f"  Vol score: {r['combined_volatility_score']} | Reversals: {wv['reversals']} | Crossed 0.5: {wv['crossed_50']}x")
    
    # Calmest matches
    print(f"\n\n=== CALMEST MATCHES (bottom 5 by volatility) ===")
    for r in results[-5:]:
        wv = r['winner_volatility']
        print(f"{r['title']} ({r['score_line']}) — vol={r['combined_volatility_score']}, range={wv['min_price']}-{wv['max_price']}")
    
    # Save to JSON for further analysis
    with open(OUTPUT, 'w') as f:
        json.dump(results, f, indent=2, default=str)
    print(f"\n\nFull data saved to {OUTPUT}")
    
    conn.close()

if __name__ == '__main__':
    main()
