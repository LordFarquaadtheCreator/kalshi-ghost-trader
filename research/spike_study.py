#!/usr/bin/env python3
"""
Find matches with "insane" price spikes: >20% moves within a couple minutes.
"""
import sqlite3, os
from collections import defaultdict

DB = os.path.expanduser("~/kalshi-ghost-trader/snapshot_for_charts.db")

def get_matches(conn):
    cur = conn.execute("""
        SELECT p.match_ticker, e.title,
               (SELECT GROUP_CONCAT(games, ' ') FROM (
                   SELECT p2.match_ticker, p2.set_number, 
                          MAX(p2.home_games) || '-' || MAX(p2.away_games) as games
                   FROM points p2 WHERE p2.match_ticker = p.match_ticker
                   GROUP BY p2.match_ticker, p2.set_number
                   ORDER BY p2.set_number
               )) as score_line
        FROM points p
        JOIN events e ON p.match_ticker = e.event_ticker
        GROUP BY p.match_ticker
        HAVING COUNT(DISTINCT p.ts_ms) > 1
           AND (SELECT COUNT(DISTINCT market_ticker) FROM ticks WHERE market_ticker LIKE p.match_ticker || '%') >= 2
        ORDER BY e.title
    """)
    return cur.fetchall()

def get_price_spikes(conn, event_ticker):
    """Find all >20% price spikes within short windows for both markets."""
    # Get market info
    cur = conn.execute("SELECT market_ticker, player_name, result FROM markets WHERE event_ticker = ?", (event_ticker,))
    mkts = {r[0]: {'name': r[1], 'result': r[2]} for r in cur.fetchall()}
    
    # Get all ticks, crop to match window
    all_ticks = conn.execute("""
        SELECT market_ticker, ts, price FROM ticks 
        WHERE market_ticker LIKE ? || '%' AND price IS NOT NULL AND ts IS NOT NULL 
        ORDER BY ts
    """, (event_ticker + '%',)).fetchall()
    
    # Get point time range to crop
    pt_ts = conn.execute("SELECT ts_ms FROM points WHERE match_ticker = ? AND ts_ms IS NOT NULL AND ts_ms > 0", (event_ticker,)).fetchall()
    if not pt_ts:
        return None, None, {}
    
    pt_min = min(r[0] for r in pt_ts) / 1000.0
    pt_max = max(r[0] for r in pt_ts) / 1000.0
    
    # Expand window a bit beyond points to catch early/late price action
    crop_start = pt_min - 7200  # 2 hours before first point
    crop_end = pt_max + 7200    # 2 hours after last point
    
    result = {}
    all_spikes = []
    
    for ticker, info in mkts.items():
        # Filter ticks to crop window
        m_ticks = [(t[1]/1000.0, t[2]) for t in all_ticks if t[0] == ticker and crop_start <= t[1]/1000.0 <= crop_end]
        
        if len(m_ticks) < 10:
            result[ticker] = None
            continue
        
        # Resample into 1-minute buckets: take last price in each minute
        buckets = {}
        for ts, price in m_ticks:
            minute = int(ts / 60)
            buckets[minute] = price  # last price wins
        
        bucket_mins = sorted(buckets.keys())
        bucket_prices = [buckets[m] for m in bucket_mins]
        
        if len(bucket_prices) < 5:
            result[ticker] = None
            continue
        
        # Compute per-minute % changes
        # Also compute 3-minute rolling change (max change over a 3-min window)
        spikes = []
        for i in range(1, len(bucket_prices)):
            p_prev = bucket_prices[i-1]
            p_cur = bucket_prices[i]
            if p_prev > 0:
                pct_change = (p_cur - p_prev) / p_prev * 100
                abs_pct = abs(pct_change)
                if abs_pct >= 15:  # 15% threshold to catch the big ones
                    spikes.append({
                        'minute': bucket_mins[i],
                        'ts_s': bucket_mins[i] * 60,
                        'price_before': round(p_prev, 3),
                        'price_after': round(p_cur, 3),
                        'pct_change': round(pct_change, 1),
                        'abs_pct': round(abs_pct, 1),
                    })
        
        # Also find 3-minute rolling max change
        rolling_3min = []
        for i in range(3, len(bucket_prices)):
            p_start = bucket_prices[i-3]
            p_end = bucket_prices[i]
            if p_start > 0:
                pct = (p_end - p_start) / p_start * 100
                rolling_3min.append(abs(pct))
        
        max_3min = max(rolling_3min) if rolling_3min else 0
        max_single = max((s['abs_pct'] for s in spikes), default=0)
        
        result[ticker] = {
            'name': info['name'],
            'result': info['result'],
            'n_ticks': len(m_ticks),
            'n_buckets': len(bucket_prices),
            'min_price': round(min(bucket_prices), 3),
            'max_price': round(max(bucket_prices), 3),
            'max_spike_pct': round(max_single, 1),
            'max_3min_move_pct': round(max_3min, 1),
            'spikes_gt_20pct': len([s for s in spikes if s['abs_pct'] >= 20]),
            'spikes_gt_15pct': len([s for s in spikes if s['abs_pct'] >= 15]),
            'worst_spike': max(spikes, key=lambda s: s['abs_pct']) if spikes else None,
            'all_spikes': spikes,
        }
        all_spikes.extend(spikes)
    
    # Find the overall worst spike across both markets
    worst_overall = max(all_spikes, key=lambda s: s['abs_pct']) if all_spikes else None
    
    return result, worst_overall

def main():
    conn = sqlite3.connect(DB)
    matches = get_matches(conn)
    
    ranked = []
    
    for match in matches:
        event_ticker, title, score_line = match[:3]
        print(f"Analyzing: {title} ({score_line})", flush=True)
        
        result, worst = get_price_spikes(conn, event_ticker)
        if not result:
            continue
        
        # Get the more interesting market (the one with bigger spikes)
        res_list = [v for v in result.values() if v is not None]
        if len(res_list) < 2:
            continue
        
        # Combined spike score
        total_spikes_20 = sum(v['spikes_gt_20pct'] for v in res_list)
        max_single = max(v['max_spike_pct'] for v in res_list)
        max_3min = max(v['max_3min_move_pct'] for v in res_list)
        
        # Score: weight by max spike + number of 20%+ moves
        spike_score = max_single * 2 + max_3min + total_spikes_20 * 10
        
        player_a = res_list[0]
        player_b = res_list[1]
        
        ranked.append({
            'title': title,
            'score_line': score_line,
            'event_ticker': event_ticker,
            'spike_score': round(spike_score, 1),
            'max_spike_pct': max_single,
            'max_3min_move_pct': max_3min,
            'spikes_gt_20pct': total_spikes_20,
            'player_a_name': player_a['name'],
            'player_a_result': player_a['result'],
            'player_a_spike': player_a['max_spike_pct'],
            'player_a_3min': player_a['max_3min_move_pct'],
            'player_a_spikes_20': player_a['spikes_gt_20pct'],
            'player_b_name': player_b['name'],
            'player_b_result': player_b['result'],
            'player_b_spike': player_b['max_spike_pct'],
            'player_b_3min': player_b['max_3min_move_pct'],
            'player_b_spikes_20': player_b['spikes_gt_20pct'],
            'worst_spike': worst,
        })
    
    # Sort by spike score descending
    ranked.sort(key=lambda r: r['spike_score'], reverse=True)
    
    # Print report
    print("\n" + "="*120)
    print("INSANE SPIKE REPORT — >20% price moves within minutes")
    print("="*120)
    
    print(f"\n{'#':<3} {'Match':<30} {'Score':<18} {'Worst Spike':<14} {'Max 3min':<10} {'20%+ Spikes':<13} {'Player A':<25} {'Player B':<25}")
    print("-"*140)
    
    for i, r in enumerate(ranked, 1):
        ws = f"{r['worst_spike']['abs_pct']}%" if r['worst_spike'] else "-"
        a = f"{r['player_a_spike']}% {'W' if r['player_a_result']=='yes' else 'L'}"
        b = f"{r['player_b_spike']}% {'W' if r['player_b_result']=='yes' else 'L'}"
        t = r['title'][:28]
        s = r['score_line'][:16]
        print(f"{i:<3} {t:<30} {s:<18} {ws:<14} {r['max_3min_move_pct']:<10} {r['spikes_gt_20pct']:<13} {a:<25} {b:<25}")
    
    # Show the worst spikes in detail for top matches
    print("\n\n=== DETAIL: TOP 10 MOST INSANE SPIKES ===\n")
    for i, r in enumerate(ranked[:10], 1):
        ws = r['worst_spike']
        print(f"{i}. {r['title']} ({r['score_line']})")
        if ws:
            print(f"   Worst spike: {ws['price_before']}¢ → {ws['price_after']}¢ ({ws['pct_change']}%) at t+{ws['minute']*60}s")
        print(f"   Player A ({r['player_a_name']}): max spike={r['player_a_spike']}%, 3min={r['player_a_3min']}%, {r['player_a_spikes_20']}x >20%")
        print(f"   Player B ({r['player_b_name']}): max spike={r['player_b_spike']}%, 3min={r['player_b_3min']}%, {r['player_b_spikes_20']}x >20%")
        
        # Show all spikes >20% chronologically
        all_sp = []
        for k in ['player_a', 'player_b']:
            if r.get(f'{k}_spikes_20', 0) > 0:
                # Get from the result dict
                pass
        print()
    
    # Spotlight: matches with genuine 20%+ spikes
    print("\n\n=== MATCHES WITH ACTUAL 20%+ SPIKES ===\n")
    has_spikes = [r for r in ranked if r['spikes_gt_20pct'] > 0]
    if has_spikes:
        for r in has_spikes:
            print(f"  ★ {r['title']} — {r['spikes_gt_20pct']}x spikes >20%, worst={r['max_spike_pct']}%")
    else:
        print("  None found at 20% threshold.")
    
    # Check lower threshold
    print(f"\n\n=== AT 15% THRESHOLD ===\n")
    
    # Re-run count at 15% by checking worst_spike
    # Actually let me fetch the detailed spike data
    # First pass already computed; let me check from stored data
    
    conn.close()

if __name__ == '__main__':
    main()
