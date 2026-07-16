import sqlite3, os, statistics
from datetime import datetime, timezone

db = os.path.expanduser("~/kalshi-ghost-trader/research/finished_slice.db")
conn = sqlite3.connect(db)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Get all events with meaningful tick data (>=100 ticks on winner market)
events = cur.execute("""
    SELECT e.event_ticker, e.title, e.series_ticker,
           myes.player_name AS winner, mno.player_name AS loser,
           myes.market_ticker AS win_ticker, mno.market_ticker AS lose_ticker,
           myes.close_ts, myes.settlement_ts,
           myes.settlement_value, mno.settlement_value,
           (SELECT COUNT(*) FROM ticks WHERE market_ticker = myes.market_ticker) AS win_ticks,
           (SELECT COUNT(*) FROM ticks WHERE market_ticker = mno.market_ticker) AS lose_ticks
    FROM events e
    JOIN markets myes ON e.event_ticker = myes.event_ticker AND myes.result = 'yes'
    JOIN markets mno  ON e.event_ticker = mno.event_ticker  AND mno.result = 'no'
    WHERE (SELECT COUNT(*) FROM ticks WHERE market_ticker = myes.market_ticker) >= 100
    ORDER BY e.event_ticker
""").fetchall()

print(f"Analyzing {len(events)} events with 100+ ticks\n")
print(f"{'Event':40s} {'Winner':25s} {'Win $ 5m':8s} {'Win $ 1m':8s} {'Win last':8s} "
      f"{'Lose $ 5m':8s} {'Lose $ 1m':8s} {'Lose last':8s} {'Fade EV':8s} {'Settle':10s}")

total_fade_ev = 0.0
total_fade_trades = 0
total_converged_by_5m = 0  # winner already at 95+ by 5m out
results = []

for ev in events:
    e = dict(ev)

    # Winner price dynamics
    win_price_5m = cur.execute("""
        SELECT AVG(price) as avg_price FROM ticks
        WHERE market_ticker = ? AND ts BETWEEN ? - 300000 AND ? - 240000
    """, (e["win_ticker"], e["close_ts"], e["close_ts"])).fetchone()[0]

    win_price_1m = cur.execute("""
        SELECT AVG(price) FROM ticks
        WHERE market_ticker = ? AND ts BETWEEN ? - 60000 AND ?
    """, (e["win_ticker"], e["close_ts"], e["close_ts"])).fetchone()[0]

    win_last = cur.execute("""
        SELECT price FROM ticks WHERE market_ticker = ?
        ORDER BY abs(ts - ?) LIMIT 1
    """, (e["win_ticker"], e["close_ts"])).fetchone()[0]

    # Loser price dynamics
    lose_price_5m = cur.execute("""
        SELECT AVG(price) FROM ticks
        WHERE market_ticker = ? AND ts BETWEEN ? - 300000 AND ? - 240000
    """, (e["lose_ticker"], e["close_ts"], e["close_ts"])).fetchone()[0]

    lose_price_1m = cur.execute("""
        SELECT AVG(price) FROM ticks
        WHERE market_ticker = ? AND ts BETWEEN ? - 60000 AND ?
    """, (e["lose_ticker"], e["close_ts"], e["close_ts"])).fetchone()[0]

    lose_last = cur.execute("""
        SELECT price FROM ticks WHERE market_ticker = ?
        ORDER BY abs(ts - ?) LIMIT 1
    """, (e["lose_ticker"], e["close_ts"])).fetchone()[0]

    win_p_5m = round(win_price_5m or 0, 4)
    win_p_1m = round(win_price_1m or 0, 4)
    win_p_last = round(win_last or 0, 4)
    lose_p_5m = round(lose_price_5m or 0, 4)
    lose_p_1m = round(lose_price_1m or 0, 4)
    lose_p_last = round(lose_last or 0, 4)

    # "Nothing Ever Happens" simulation:
    # At 5m before close, buy the loser's No (which = winner's Yes)
    # This means: buy the winner at market price, knowing it'll resolve to $1
    # Simpler: buy the loser's yes contract (it's ~lose_price_5m)
    # and since loser loses, that contract goes to 0.

    # Actually, "Nothing Ever Happens" = bet AGAINST longshots.
    # In tennis, at 5m out, the loser's Yes is the longshot.
    # Buy the loser's No (= winner's Yes) = buy winner at lose_5m_implied_no_price
    # Lose No price = 1.00 - lose_yes_price

    # If loser's Yes is at lose_price_5m (e.g., 0.05), then:
    # Loser No = 0.95, which resolves to $1 when loser loses (95% of the time in theory)
    # ROI on loser No buy = ($1 - buy_price) / buy_price = (1 - 0.95) / 0.95 = 5.3%

    if lose_price_5m and lose_price_5m > 0 and lose_price_5m < 0.5:
        # Buy the No on the loser = 1 - lose_yes_price
        buy_price = 1.0 - lose_price_5m
        # Since loser loses, No resolves to $1
        fade_roi = (1.0 - buy_price) / buy_price * 100
        fade_ev = 1.0 - buy_price  # profit per $1 notional
    else:
        buy_price = 0
        fade_roi = 0
        fade_ev = 0

    total_fade_ev += fade_ev
    if fade_roi > 0:
        total_fade_trades += 1

    if win_price_5m and win_price_5m >= 0.95:
        total_converged_by_5m += 1

    results.append({
        "event": e["event_ticker"][:38],
        "winner": e["winner"][:24],
        "win_5m": win_p_5m,
        "win_1m": win_p_1m,
        "win_last": win_p_last,
        "lose_5m": lose_p_5m,
        "lose_1m": lose_p_1m,
        "lose_last": lose_p_last,
        "fade_roi": fade_roi,
        "fade_ev": fade_ev,
    })

    # Check if maxing data - we want the print too
    series_str = e["series_ticker"]
    print(f"{e['event_ticker'][:38]:38s} {e['winner'][:24]:24s} "
          f"{str(win_p_5m):>8s} {str(win_p_1m):>8s} {str(win_p_last):>8s} "
          f"{str(lose_p_5m):>8s} {str(lose_p_1m):>8s} {str(lose_p_last):>8s} "
          f"{f'+{fade_roi:.1f}%' if fade_roi > 0 else '  -   ':>8s} "
          f"${e['close_ts']/1000:%H:%M}")

print(f"\n--- Summary ---")
print(f"Events analyzed:        {len(events)}")
print(f"Winner >95¢ by 5m out:  {total_converged_by_5m}/{len(events)} (market already priced)")

# Average profit from fading the loser at 5m out
trades_with_edge = [r for r in results if r["fade_roi"] > 0]
avg_roi = statistics.mean([r["fade_roi"] for r in trades_with_edge]) if trades_with_edge else 0
total_profit = sum(r["fade_ev"] for r in trades_with_edge)
print(f"Trades with fade edge:  {len(trades_with_edge)}/{len(events)}")
print(f"Avg fade ROI:           {avg_roi:.2f}%")
print(f"Total EV ($1/contract): ${total_profit:.4f} per position")

# The cleaner analysis: what was the win price at 5m out?
avg_win_at_5m = statistics.mean([r["win_5m"] for r in results if r["win_5m"]])
avg_lose_at_5m = statistics.mean([r["lose_5m"] for r in results if r["lose_5m"]])
print(f"Avg winner price @5m:   {avg_win_at_5m:.4f} (${avg_win_at_5m:.2f})")
print(f"Avg loser price @5m:    {avg_lose_at_5m:.4f} (${avg_lose_at_5m:.2f})")

# How much profit if you just bought the winner at 5m out?
avg_winner_buy_roi = (1.0 - avg_win_at_5m) / avg_win_at_5m * 100 if avg_win_at_5m else 0
print(f"Buy winner @5m -> $1 ROI:  {avg_winner_buy_roi:.2f}%")

conn.close()
