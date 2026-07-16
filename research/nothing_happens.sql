-- Analyze price convergence in final 5 min for all events with 100+ ticks on winner
.echo off
.header on
.mode column

-- For each event, get price snapshot at 5m out vs final min
SELECT e.event_ticker,
  myes.player_name AS winner,
  ROUND(avg_win_5m, 4) AS win_price_5m_out,
  ROUND(avg_win_1m, 4) AS win_price_final_min,
  ROUND(last_win_price, 4) AS win_last_price,
  ROUND(avg_lose_5m, 4) AS lose_price_5m_out,
  ROUND(avg_lose_1m, 4) AS lose_price_final_min,
  ROUND(last_lose_price, 4) AS lose_last_price,
  win_ticks, lose_ticks
FROM (
  SELECT e.event_ticker,
    myes.player_name,
    (SELECT AVG(price) FROM ticks WHERE market_ticker = myes.market_ticker
      AND ts BETWEEN myes.close_ts - 300000 AND myes.close_ts - 240000) AS avg_win_5m,
    (SELECT AVG(price) FROM ticks WHERE market_ticker = myes.market_ticker
      AND ts BETWEEN myes.close_ts - 60000 AND myes.close_ts) AS avg_win_1m,
    (SELECT price FROM ticks WHERE market_ticker = myes.market_ticker
      ORDER BY abs(ts - myes.close_ts) LIMIT 1) AS last_win_price,
    (SELECT AVG(price) FROM ticks WHERE market_ticker = mno.market_ticker
      AND ts BETWEEN myes.close_ts - 300000 AND myes.close_ts - 240000) AS avg_lose_5m,
    (SELECT AVG(price) FROM ticks WHERE market_ticker = mno.market_ticker
      AND ts BETWEEN myes.close_ts - 60000 AND myes.close_ts) AS avg_lose_1m,
    (SELECT price FROM ticks WHERE market_ticker = mno.market_ticker
      ORDER BY abs(ts - myes.close_ts) LIMIT 1) AS last_lose_price,
    (SELECT COUNT(*) FROM ticks WHERE market_ticker = myes.market_ticker) AS win_ticks,
    (SELECT COUNT(*) FROM ticks WHERE market_ticker = mno.market_ticker) AS lose_ticks
  FROM events e
  JOIN markets myes ON e.event_ticker = myes.event_ticker AND myes.result = 'yes'
  JOIN markets mno  ON e.event_ticker = mno.event_ticker  AND mno.result = 'no'
  WHERE (SELECT COUNT(*) FROM ticks WHERE market_ticker = myes.market_ticker) >= 100
)
ORDER BY event_ticker;
