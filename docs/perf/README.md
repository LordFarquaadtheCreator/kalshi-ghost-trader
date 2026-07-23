# Performance Profiling Artifacts

This directory holds captured profiles and query stats for performance work
(playbook Task R.5). Each subdirectory is dated `YYYY-MM-DD`.

## Required artifacts per capture

1. **CPU profile (60s)** — `go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=60`
2. **Heap profile** — `go tool pprof http://127.0.0.1:6060/debug/pprof/heap`
3. **Top queries (full day)** — `psql -U kalshi kalshi_tennis -f scripts/top-queries.sql`

Capture during an active match window for representative hot-path data.

## pg_stat_statements setup (one-time on deploy box)

```sql
-- In postgresql.conf:
--   shared_preload_libraries = 'pg_stat_statements'
-- Then restart Postgres, then:
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

```bash
ssh mint 'sudo -n systemctl restart postgresql'
```

## Per-hop latency histograms

R.5.3 adds two slog-exported histograms on the hot path, logged every 60s:

- `ws_recv_to_strategy_done` — WS message receive → all strategies processed (hop1)
- `strategy_done_to_order_persisted` — order emitted → batch flushed to DB (hop2)

No external metrics dependency. Search logs:
```bash
ssh mint 'sudo -n journalctl -u kalshi-ghost-trader --no-pager -n 1000 | grep "perf histogram"'
```
