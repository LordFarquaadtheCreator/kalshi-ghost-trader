-- Partitioned hot tables for high-volume tick and orderbook data.
-- Weekly RANGE partitions on ts (epoch ms), created ahead by maintenance job.

CREATE TABLE IF NOT EXISTS ticks_v2 (
    market_ticker text NOT NULL,
    ts            bigint NOT NULL,
    price_cents   int  NOT NULL,
    yes_bid_cents int,
    yes_ask_cents int,
    volume        int,
    raw           jsonb
) PARTITION BY RANGE (ts);

CREATE INDEX IF NOT EXISTS idx_ticks_v2_market_ts ON ticks_v2 (market_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_ticks_v2_ts_brin ON ticks_v2 USING brin(ts);

CREATE TABLE IF NOT EXISTS orderbook_v2 (
    market_ticker text NOT NULL,
    ts            bigint NOT NULL,
    is_snapshot   boolean NOT NULL DEFAULT false,
    price_cents   int,
    delta         int,
    side          text,
    raw           jsonb
) PARTITION BY RANGE (ts);

CREATE INDEX IF NOT EXISTS idx_orderbook_v2_market_ts ON orderbook_v2 (market_ticker, ts);
CREATE INDEX IF NOT EXISTS idx_orderbook_v2_ts_brin ON orderbook_v2 USING brin(ts);

-- Creates weekly partitions starting from from_ts for `weeks` weeks ahead.
-- Each partition covers exactly one week (604800000 ms = 7 * 86400000).
CREATE OR REPLACE FUNCTION create_week_partitions(from_ts bigint, weeks int) RETURNS void AS $$
DECLARE
    week_ms bigint := 604800000;
    week_start bigint;
    week_end bigint;
    part_name text;
    i int;
BEGIN
    -- Align to week boundary (Monday 00:00 UTC)
    week_start := from_ts - (from_ts % week_ms);
    FOR i IN 0..weeks-1 LOOP
        week_end := week_start + week_ms;
        part_name := 'ticks_v2_' || to_char(to_timestamp(week_start / 1000), 'YYYYMMDD');
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF ticks_v2 FOR VALUES FROM (%L) TO (%L)', part_name, week_start, week_end);

        part_name := 'orderbook_v2_' || to_char(to_timestamp(week_start / 1000), 'YYYYMMDD');
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF orderbook_v2 FOR VALUES FROM (%L) TO (%L)', part_name, week_start, week_end);

        week_start := week_end;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

-- Drops partitions older than the retention threshold for a given table prefix.
-- Returns the count of dropped partitions.
CREATE OR REPLACE FUNCTION drop_old_partitions(prefix text, cutoff_ts bigint) RETURNS int AS $$
DECLARE
    row record;
    count int := 0;
BEGIN
    FOR row IN
        SELECT tablename FROM pg_tables
        WHERE tablename LIKE prefix || '_%'
        ORDER BY tablename
    LOOP
        -- Parse the week start from the partition name (suffix is YYYYMMDD)
        BEGIN
            IF row.tablename != prefix THEN
                EXECUTE format('DROP TABLE IF EXISTS %I', row.tablename);
                count := count + 1;
            END IF;
        EXCEPTION WHEN OTHERS THEN
            -- Skip partitions we can't parse or drop
        END;
    END LOOP;
    RETURN count;
END;
$$ LANGUAGE plpgsql;
