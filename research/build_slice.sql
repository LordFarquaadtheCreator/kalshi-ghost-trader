ATTACH DATABASE '/Users/farquaad/kalshi-ghost-trader/research/finished_slice.db' AS slice;

CREATE TABLE slice.events AS
SELECT e.* FROM main.events e
JOIN main.markets m ON e.event_ticker = m.event_ticker AND m.result = 'yes' AND m.settlement_value = '1.0000'
WHERE m.status = 'finalized'
AND EXISTS (
  SELECT 1 FROM main.markets m2
  WHERE m2.event_ticker = e.event_ticker AND m2.result = 'no' AND m2.settlement_value = '0.0000'
);

CREATE TABLE slice.markets AS
SELECT m.* FROM main.markets m
WHERE m.event_ticker IN (SELECT event_ticker FROM slice.events)
AND m.status = 'finalized';

CREATE TABLE slice.ticks AS
SELECT t.* FROM main.ticks t
WHERE t.market_ticker IN (SELECT market_ticker FROM slice.markets)
AND t.ts >= (
  SELECT m.close_ts FROM main.markets m
  WHERE m.market_ticker = t.market_ticker LIMIT 1
) - 300000;

CREATE INDEX slice.idx_ticks_market_ts ON slice.ticks(market_ticker, ts);
CREATE INDEX slice.idx_markets_event ON slice.markets(event_ticker);
CREATE INDEX slice.idx_markets_result ON slice.markets(result);

SELECT 'Events' AS tbl, COUNT(*) AS cnt FROM slice.events
UNION ALL
SELECT 'Markets', COUNT(*) FROM slice.markets
UNION ALL
SELECT 'Winning markets (yes)', COUNT(*) FROM slice.markets WHERE result = 'yes'
UNION ALL
SELECT 'Losing markets (no)', COUNT(*) FROM slice.markets WHERE result = 'no'
UNION ALL
SELECT 'Ticks', COUNT(*) FROM slice.ticks;

DETACH slice;
