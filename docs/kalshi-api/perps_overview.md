# Perps API (Margin / Perpetual Futures)

> Getting started with Kalshi's perpetual-futures trading API

"Perps", "margin", and "perpetual futures" all refer to the same product. The API surface uses *margin* throughout (endpoints under `/margin` namespace, margin-prefixed fields).

Mirrors the event contract API — same patterns, authentication, conventions, just under `/margin`.

## Connectivity

### REST API

| Environment | Base URL                                       |
| ----------- | ---------------------------------------------- |
| Demo        | `https://external-api.demo.kalshi.co/trade-api/v2/margin/`  |
| Production  | `https://external-api.kalshi.com/trade-api/v2/margin/`      |

### WebSocket API

| Environment | URL                                                       |
| ----------- | --------------------------------------------------------- |
| Demo        | `wss://external-api-margin-ws.demo.kalshi.co/trade-api/ws/v2/margin`  |
| Production  | `wss://external-api-margin-ws.kalshi.com/trade-api/ws/v2/margin`      |

### FIX API

Separate host from event contract FIX.

| Environment | Type                      | Host                                         |
| ----------- | ------------------------- | -------------------------------------------- |
| Demo        | Order entry and drop copy | `margin-fix.demo.kalshi.co`                  |
| Demo        | Market data               | `margin-marketdata.fix.demo.kalshi.co`       |
| Production  | Order entry and drop copy | `margin-mm.fix.elections.kalshi.com`         |
| Production  | Market data               | `margin-marketdata.fix.elections.kalshi.com` |

Session types:

| Purpose                              | Port | TargetCompID |
| ------------------------------------ | ---- | ------------ |
| Order Entry (without retransmission) | 8228 | KalshiNR     |
| Drop Copy                            | 8229 | KalshiDC     |
| Order Entry (with retransmission)    | 8230 | KalshiRT     |
| Market Data                          | 8233 | KalshiMD     |

## API Reference

### REST Endpoints

All under `/margin/` prefix. See `perps_openapi.yaml` for full spec.

**Exchange**: `GET /margin/exchange/status`, `GET /margin/exchange/enabled`

**Markets**: `GET /margin/markets`, `GET /margin/markets/{ticker}`, `GET /margin/markets/{ticker}/orderbook`, `GET /margin/markets/{ticker}/candlesticks`, `GET /margin/markets/trades`

**Orders**: `POST /margin/orders`, `DELETE /margin/orders/{order_id}`, `POST /margin/orders/{order_id}/amend`, `POST /margin/orders/{order_id}/decrease`, `GET /margin/orders`, `GET /margin/orders/{order_id}`

**Portfolio**: `GET /margin/portfolio/balance`, `GET /margin/portfolio/positions`, `GET /margin/portfolio/fills`, `POST /margin/portfolio/subaccounts`, `POST /margin/portfolio/subaccounts/transfer`

**Risk**: `GET /margin/risk`, `GET /margin/risk/parameters`, `GET /margin/risk/notional-limit`

**Funding**: `GET /margin/funding/history`, `GET /margin/funding/estimate`, `GET /margin/funding/rates`

**Fees**: `GET /margin/fee_tiers`

**Order Groups**: Same CRUD as event contracts under `/margin/order_groups/`

### WebSocket Channels

All under `/margin` WS path. See `perps_asyncapi.yaml` for full spec.

- `margin_ticker` — market price, volume, open interest, mark prices
- `orderbook` — orderbook snapshots + deltas
- `user_orders` — private order notifications
- `user_fills` — private fill notifications
- `public_trades` — public trade notifications
- `order_group_updates` — order group lifecycle

### Key Differences from Event Contracts

- **Funding**: Perps have periodic funding payments. `GET /margin/funding/estimate` for current period estimate.
- **Mark Price**: Market responses include `mark_price` and `mark_price_ts`.
- **Risk**: Leverage, liquidation prices, maintenance margin. `GET /margin/risk` for per-position data.
- **Portfolio Margin**: `is_portfolio` flag on positions — when true, per-position risk metrics not reported (jointly margined).
- **Volume/OI Notional**: Dollar notional companions for volume and open interest fields.
- **Tick Size**: `tick_size` field on market responses.
- **Price Banding**: See [Price Banding](perps_price_banding.md) for margin market price bands.

## Rate Limits

Perps use **separate token buckets** from event contracts. Four independent buckets total: event-contract Read/Write, perps Read/Write.

Check perps tier via `GET /account/limits/perps`.
