# Kalshi API Documentation

> Complete local mirror of Kalshi API docs, specs, and references.

## Specifications

| Spec | File | Description |
|------|------|-------------|
| Predictions OpenAPI | [openapi.yaml](openapi.yaml) | REST API spec v3.25.0 — all event-contract endpoints |
| Predictions AsyncAPI | [asyncapi.yaml](asyncapi.yaml) | WebSocket API spec — all event-contract channels |
| Perps OpenAPI | [perps_openapi.yaml](perps_openapi.yaml) | REST API spec — all margin/perps endpoints |
| Perps AsyncAPI | [perps_asyncapi.yaml](perps_asyncapi.yaml) | WebSocket API spec — all margin/perps channels |

## Getting Started

| Doc | File | Topics |
|-----|------|--------|
| Welcome | [welcome.md](welcome.md) | API overview, doc structure |
| API Keys | [gs_api_keys.md](gs_api_keys.md) | RSA key pairs, request signing, headers |
| API Environments | [gs_api_environments.md](gs_api_environments.md) | Production/demo URLs |
| Demo Environment | [gs_demo_env.md](gs_demo_env.md) | Demo setup, mock funds |
| First Request | [gs_making_your_first_request.md](gs_making_your_first_request.md) | Public endpoints starter |
| Terms | [gs_terms.md](gs_terms.md) | Category, Subcategory, Market, Event, Series |
| Rate Limits | [gs_rate_limits.md](gs_rate_limits.md) | Token buckets, tiers, bursting, grants |
| Fee Rounding | [gs_fee_rounding.md](gs_fee_rounding.md) | Subpenny precision, fee accumulator |
| Fixed-Point Migration | [gs_fixed_point_migration.md](gs_fixed_point_migration.md) | `_dollars`, `_fp` suffixes, price level structures |
| Market Lifecycle | [gs_market_lifecycle.md](gs_market_lifecycle.md) | Statuses, transitions, settlement |
| Market Settlement | [gs_market_settlement.md](gs_market_settlement.md) | Outcome determination, position resolution |
| Maintenance & Pauses | [gs_maintenance_and_pauses.md](gs_maintenance_and_pauses.md) | Thursday 3-5am ET, trading vs exchange pause |
| Order Direction | [gs_order_direction.md](gs_order_direction.md) | `outcome_side` / `book_side` migration |
| Order Groups | [gs_order_groups.md](gs_order_groups.md) | Rolling contract limits, auto-cancel |
| Orderbook Responses | [gs_orderbook_responses.md](gs_orderbook_responses.md) | Binary market orderbook structure |
| Pagination | [gs_pagination.md](gs_pagination.md) | Cursor-based pagination |
| Historical Data | [gs_historical_data.md](gs_historical_data.md) | Cutoff timestamps, archived data |
| RFQs | [gs_rfqs.md](gs_rfqs.md) | Request for Quote flow, sizing, timing |
| Subaccounts | [gs_subaccounts.md](gs_subaccounts.md) | Balance/position partitioning (0-63) |
| Targets & Milestones | [gs_targets_and_milestones.md](gs_targets_and_milestones.md) | Metadata for grouping events |

### Quick Start Guides

| Guide | File |
|-------|------|
| Authenticated Requests | [gs_quick_start_authenticated_requests.md](gs_quick_start_authenticated_requests.md) |
| Create Order | [gs_quick_start_create_order.md](gs_quick_start_create_order.md) |
| Market Data | [gs_quick_start_market_data.md](gs_quick_start_market_data.md) |
| WebSockets | [gs_quick_start_websockets.md](gs_quick_start_websockets.md) |

## REST API Reference

| Doc | File | Description |
|-----|------|-------------|
| Predictions REST | [rest_api_reference.md](rest_api_reference.md) | All event-contract endpoints from OpenAPI spec |

## WebSocket API

| Doc | File | Description |
|-----|------|-------------|
| WS Index | [ws_index.md](ws_index.md) | Channel overview |
| WS Connection | [ws_connection.md](ws_connection.md) | Handshake, auth, subscription protocol |
| Keep-Alive | [ws_keepalive.md](ws_keepalive.md) | Ping/pong heartbeat |
| Market Ticker | [ws_market_ticker.md](ws_market_ticker.md) | Price, volume, OI updates |
| Orderbook | [ws_orderbook.md](ws_orderbook.md) | Orderbook snapshots + deltas |
| Market & Event Lifecycle | [ws_market_event_lifecycle.md](ws_market_event_lifecycle.md) | State changes, event creation |
| MVE Lifecycle | [ws_mve_lifecycle.md](ws_mve_lifecycle.md) | Multivariate event lifecycle |
| Market Positions | [ws_market_positions.md](ws_market_positions.md) | Position updates |
| Public Trades | [ws_public_trades.md](ws_public_trades.md) | Trade notifications |
| User Fills | [ws_user_fills.md](ws_user_fills.md) | Private fill notifications |
| User Orders | [ws_user_orders.md](ws_user_orders.md) | Private order notifications |
| Order Groups | [ws_order_groups.md](ws_order_groups.md) | Order group lifecycle |
| Communications | [ws_communications.md](ws_communications.md) | RFQ/quote notifications |
| Pyth Value Feed | [ws_pyth_value.md](ws_pyth_value.md) | Real-time Pyth price updates |

## FIX API

| Doc | File | Description |
|-----|------|-------------|
| FIX Overview | [fix_overview.md](fix_overview.md) | Connectivity, sessions, order entry, market data, RFQ |

## Perps (Margin) API

| Doc | File | Description |
|-----|------|-------------|
| Perps Overview | [perps_overview.md](perps_overview.md) | REST/WS/FIX connectivity, endpoints, key differences |
| Price Banding | [perps_price_banding.md](perps_price_banding.md) | Bid/ask band limits for margin markets |

## SDKs

| Doc | File | Description |
|-----|------|-------------|
| SDKs Overview | [sdks_overview.md](sdks_overview.md) | Python (sync/async), TypeScript packages |

## External Data Feeds

| Doc | File | Description |
|-----|------|-------------|
| CF Benchmarks | [cfbenchmarks.md](cfbenchmarks.md) | REST passthrough + WS value feed |

## Changelog

| Doc | File | Description |
|-----|------|-------------|
| API Changelog | [changelog.md](changelog.md) | REST/WS/FIX changes, version history |

## Environment URLs

### Predictions (Event Contracts)

| Type | Production | Demo |
|------|-----------|------|
| REST | `https://external-api.kalshi.com/trade-api/v2` | `https://external-api.demo.kalshi.co/trade-api/v2` |
| WebSocket | `wss://external-api-ws.kalshi.com/trade-api/ws/v2` | `wss://external-api-ws.demo.kalshi.co/trade-api/ws/v2` |
| FIX OE | `mm.fix.elections.kalshi.com` | `fix.demo.kalshi.co` |
| FIX MD | `marketdata.fix.elections.kalshi.com` | `marketdata.fix.demo.kalshi.co` |

### Perps (Margin)

| Type | Production | Demo |
|------|-----------|------|
| REST | `https://external-api.kalshi.com/trade-api/v2/margin/` | `https://external-api.demo.kalshi.co/trade-api/v2/margin/` |
| WebSocket | `wss://external-api-margin-ws.kalshi.com/trade-api/ws/v2/margin` | `wss://external-api-margin-ws.demo.kalshi.co/trade-api/ws/v2/margin` |
| FIX OE | `margin-mm.fix.elections.kalshi.com` | `margin-fix.demo.kalshi.co` |
| FIX MD | `margin-marketdata.fix.elections.kalshi.com` | `margin-marketdata.fix.demo.kalshi.co` |

## Authentication

All authenticated requests use RSA-PSS-SHA256 signed headers:

- `KALSHI-ACCESS-KEY`: API Key ID (UUID)
- `KALSHI-ACCESS-TIMESTAMP`: Current Unix timestamp in milliseconds
- `KALSHI-ACCESS-SIGNATURE`: Base64 RSA-PSS signature of `timestamp + method + path`

Path excludes query parameters. Same key pair works for REST, WebSocket, and FIX.

See [API Keys](gs_api_keys.md) for details.
