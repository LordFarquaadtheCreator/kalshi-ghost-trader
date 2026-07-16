-- Preview what will be deleted
.mode column
.header on

SELECT e.event_ticker, e.series_ticker, m.player_name,
       COUNT(t.id) as tick_cnt,
       ROUND((MAX(t.ts)-MIN(t.ts))/1000.0, 0) as span_s,
       (SELECT COUNT(*) FROM points p WHERE p.match_ticker = e.event_ticker) as pts,
       CASE
           WHEN COUNT(t.id) < 10 THEN 'drive-by'
           WHEN (MAX(t.ts)-MIN(t.ts))/1000.0 < 120 THEN 'late joiner'
           ELSE 'keeper'
       END as reason
FROM events e
JOIN markets m ON e.event_ticker = m.event_ticker AND m.result = 'yes'
JOIN ticks t ON t.market_ticker = m.market_ticker
WHERE t.ts >= m.close_ts - 300000 AND t.ts <= m.settlement_ts
GROUP BY e.event_ticker
HAVING reason IN ('drive-by', 'late joiner')
ORDER BY reason, tick_cnt DESC;
