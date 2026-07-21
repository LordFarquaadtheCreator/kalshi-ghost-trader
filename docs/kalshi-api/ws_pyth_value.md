# Pyth Value Feed (WebSocket)

> Real-time Pyth price updates for configured underlying tickers

Requires authentication. Deduplicated real-time Pyth prices.

## Subscription

- Channel: `pyth_value`
- Seed `underlying_tickers` on subscribe, or add later
- Use `underlying_tickers: ["all"]` for all available
- `update_subscription` with `subscribe_underlyings` / `unsubscribe_underlyings`
- `underlying_list` action returns recently streamed underlyings (last 2 hours)
- Subscribe without `underlying_tickers` for empty subscription, then discover and add

## Message: Pyth Value Update

```json
{
  "type": "pyth_value",
  "sid": 1,
  "seq": 42,
  "msg": {
    "underlying_ticker": "Metal.XAU/USD",
    "value_usd": "2365.12345000",
    "source_ts_ms": 1710000000100,
    "received_at": 1710000000123
  }
}
```

| Field | Description |
|-------|-------------|
| `underlying_ticker` | Qualified Pyth underlying ticker |
| `value_usd` | USD value, 8 decimal places |
| `source_ts_ms` | Pyth source timestamp (unix ms) |
| `received_at` | When Kalshi received update (unix ms) |

Duplicate and out-of-order source timestamps ignored independently per underlying ticker.

## Message: Pyth Underlying List

```json
{
  "type": "pyth_value_underlying_list",
  "id": 2,
  "sid": 1,
  "seq": 1,
  "msg": {
    "underlying_tickers": ["Metal.XAG/USD", "Metal.XAU/USD"]
  }
}
```

Returns underlying tickers observed on Pyth stream in last 2 hours.
