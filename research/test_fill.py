import sqlite3, os

db = os.path.expanduser("~/kalshi-ghost-trader/research/finished_slice.db")
conn = sqlite3.connect(db)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

events = cur.execute("""
    SELECT e.event_ticker, myes.market_ticker, myes.close_ts
    FROM events e
    JOIN markets myes ON e.event_ticker = myes.event_ticker AND myes.result = 'yes'
    WHERE (SELECT COUNT(*) FROM ticks WHERE market_ticker = myes.market_ticker) >= 100
    ORDER BY e.event_ticker
""").fetchall()

print(f"{'Event':38s} {'Lowest $':8s} {'Sold 95?':8s} {'Sold 90?':8s} {'Sold 99?':8s} {'At 99→close':10s}")
print("-" * 82)

would_fill_95 = would_fill_90 = would_fill_99 = 0
total = len(events)

for ev in events:
    ticker = ev["market_ticker"]
    close_ts = ev["close_ts"]

    cursor2 = conn.cursor()
    min_all = cursor2.execute("""
        SELECT MIN(price) FROM ticks
        WHERE market_ticker = ? AND ts BETWEEN ? - 300000 AND ?
    """, (ticker, close_ts, close_ts)).fetchone()[0]

    min_at_99_ts = cursor2.execute("""
        SELECT MIN(ts) FROM ticks
        WHERE market_ticker = ? AND price >= 0.99 AND ts BETWEEN ? - 300000 AND ?
    """, (ticker, close_ts, close_ts)).fetchone()[0]

    f95 = min_all <= 0.95
    f90 = min_all <= 0.90
    f99 = min_all <= 0.99

    if f95: would_fill_95 += 1
    if f90: would_fill_90 += 1
    if f99: would_fill_99 += 1

    t99 = f"{(close_ts - min_at_99_ts)/1000:4.0f}s" if min_at_99_ts else "  NEVER"

    print(f"{ev['event_ticker'][:36]:36s} ${min_all:<6.3f} "
          f"{'YES':>6s} {'YES' if f90 else 'NO':>6s} {'YES' if f99 else 'NO':>6s} {t99:>10s}")

print(f"\n--- Fill Rates at Each Price ---")
print(f"Buy at 95¢:  fills {would_fill_95}/{total} ({would_fill_95/total*100:.0f}%) → 5¢/contract profit each")
print(f"Buy at 90¢:  fills {would_fill_90}/{total} ({would_fill_90/total*100:.0f}%) → 10¢/contract profit each")
print(f"Buy at 99¢:  fills {would_fill_99}/{total} ({would_fill_99/total*100:.0f}%) → 1¢/contract profit each")
print()
print("All fills win at $1. The tradeoff: lower price = less fills but more profit per fill.")

conn.close()
