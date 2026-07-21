# CF Benchmarks

## REST Passthrough

Query CF Benchmarks REST API using Kalshi API credentials. No separate CF Benchmarks key needed.

### Base URL

```
https://external-api.kalshi.com/trade-api/v2/cfbenchmarks
```

Everything after `/cfbenchmarks/` forwarded to `https://www.cfbenchmarks.com/api/v1/`.

### Rate Limit

50 tokens per request from Read bucket (default is 10).

### Example

```bash
curl "https://external-api.kalshi.com/trade-api/v2/cfbenchmarks/values?id=BRTI" \
  -H "KALSHI-ACCESS-KEY: <key>" \
  -H "KALSHI-ACCESS-SIGNATURE: <sig>" \
  -H "KALSHI-ACCESS-TIMESTAMP: <ts>"
```

Response wraps upstream payload in `data` field.

### Error Mapping

| Condition | Kalshi Response |
|-----------|----------------|
| Not found upstream | 404 not_found |
| Upstream rate limit | 429 too_many_requests |
| Upstream auth/server error | 503 service_unavailable |
| Other upstream client error | 400 bad_request |

## WebSocket: CF Benchmarks Value Feed

Real-time CF Benchmarks index value updates. Requires authentication.

### Subscription

- Channel: `cfbenchmarks_value`
- Seed `index_ids` array (e.g. `["BRTI", "ETHUSD_RTI"]`) on subscribe, or add later
- Use `index_ids: ["all"]` for all available indices
- `indexlist` action returns available index IDs without modifying subscription
- `update_subscription` with `subscribe_indices` / `unsubscribe_indices`

### Averaging Semantics

`avg_60s_data` (always present):
- Trailing window: `[source_ts_ms - 60000, source_ts_ms)`
- Falls back to current tick if no prior ticks in window

`last_60s_windowed_average_15min` (only in final minute before quarter-hour close at `:00`, `:15`, `:30`, `:45`):
- Active window: `(quarter_close_ts_ms - 60000, quarter_close_ts_ms]`
- Start boundary excluded, close tick included

### Message Fields

- `type`: `cfbenchmarks_value`
- `sid`: subscription ID
- `seq`: sequence number
- `msg.index_id`: CF Benchmarks index ID
- `msg.value`: index value
- `msg.source_ts_ms`: upstream timestamp
- `msg.avg_60s_data`: trailing 60s average data
- `msg.last_60s_windowed_average_15min`: quarter-hour final-minute average (when active)
