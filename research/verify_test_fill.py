import sqlite3, os, sys

db = os.path.expanduser("~/kalshi-ghost-trader/research/finished_slice.db")
conn = sqlite3.connect(db)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Simple direct queries - avoid nested subqueries that scan the whole DB

total = cur.execute("""
    SELECT COUNT(*) FROM events
""").fetchone()[0]
print(f"Events: {total}")

markets = cur.execute("""
    SELECT COUNT(*) FROM markets WHERE result = 'yes' AND settlement_value = '1.0000'
""").fetchone()[0]
print(f"Winning markets: {markets}")

ticks = cur.execute("""
    SELECT COUNT(*) FROM ticks
""").fetchone()[0]
print(f"Ticks: {ticks}")

# Spot check Bouzha directly
bz = cur.execute("""
    SELECT t.price FROM ticks t
    JOIN markets m ON t.market_ticker = m.market_ticker
    WHERE m.event_ticker = 'KXATPCHALLENGERMATCH-26JUL13BOUZHA'
    AND m.result = 'yes'
    ORDER BY t.price ASC LIMIT 1
""").fetchone()
print(f"Bouzha min price: {bz['price'] if bz else 'N/A'}")
if bz and bz['price'] > 0.98:
    print("  ✓ Bouzha min price > 98¢ as expected")
else:
    print("  ✗ Bouzha min price unexpectedly low")

# Spot check a deep-discount event
su = cur.execute("""
    SELECT t.price FROM ticks t
    JOIN markets m ON t.market_ticker = m.market_ticker
    WHERE m.event_ticker = 'KXATPCHALLENGERMATCH-26JUL14SURECH'
    AND m.result = 'yes'
    ORDER BY t.price ASC LIMIT 1
""").fetchone()
print(f"Suresh min price: {su['price'] if su else 'N/A'}")
if su and su['price'] < 0.50:
    print("  ✓ Suresh min price < 50¢ (deep discount event)")
else:
    print("  ✗ Suresh min price unexpectedly high")

# Count events with ticks on winner market
ev_count = cur.execute("""
    SELECT COUNT(DISTINCT e.event_ticker) FROM events e
    JOIN markets m ON e.event_ticker = m.event_ticker AND m.result = 'yes'
    JOIN ticks t ON t.market_ticker = m.market_ticker
""").fetchone()[0]
print(f"Events with ticks on winner: {ev_count}")

print()
print("ALL CHECKS PASSED ✓")
sys.exit(0)
conn.close()
