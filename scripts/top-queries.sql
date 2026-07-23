-- Top 20 queries by total execution time from pg_stat_statements.
-- Requires: shared_preload_libraries = 'pg_stat_statements' in postgresql.conf
-- and CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
--
-- Reset counters between capture windows:
--   SELECT pg_stat_statements_reset();
SELECT
    substring(query, 1, 120) AS query,
    calls,
    round(total_exec_time::numeric, 2)     AS total_ms,
    round(mean_exec_time::numeric, 2)      AS mean_ms,
    round(max_exec_time::numeric, 2)       AS max_ms,
    rows,
    round((shared_blks_hit::numeric /
        NULLIF(shared_blks_hit + shared_blks_read, 0) * 100), 1) AS hit_pct,
    shared_blks_read                       AS blocks_read
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20;
