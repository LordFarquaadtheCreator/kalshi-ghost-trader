# API Changelog

> Stay updated with API changes and version history

RSS feed: `/changelog/rss.xml`

Covers Kalshi's REST, WebSocket, and FIX APIs across both Predictions and Margin exchanges.

## Recent Changes (2026)

### July 23, 2026

- **WebSocket (Predictions, Margin)**: Subaccount-restricted API keys can now open WebSocket sessions. Private channels scoped to key's locked subaccount.
- **FIX (Predictions)**: Subaccount-restricted API keys can log on to RfqMode FIX sessions and run maker quote lifecycle.
- **WebSocket (Predictions)**: New `pyth_value` channel for deduplicated Pyth price updates by underlying ticker.
- **REST, WebSocket (Predictions)**: Seven new `price_level_structure` values introduced (`center_whole_edge_half_cent`, `center_whole_edge_quint_cent`, `center_half_edge_half_cent`, `center_half_edge_quint_cent`, `center_half_edge_deci_cent`, `center_quint_edge_quint_cent`, `center_quint_edge_deci_cent`). Consume `price_ranges` array dynamically. Pilot markets switch week of July 27.

### July 9, 2026

- **FIX (Predictions, Margin)**: FIX Tag 2446 (`AggressorSide`) supported on `35=X` Incremental Refresh with `MDEntryType=2` (Trade).
- **REST (Predictions)**: RFQ-scoped quote lookup endpoint. Quote-ID-only lookup deprecated.
- **REST (Predictions)**: Deprecated fields removed: `Market.response_price_units`, `Market.fractional_trading_enabled`, `MarketPosition.resting_orders_count`.
- **REST (Margin)**: `GET /margin/orders` now includes `order_reason` when `order_source` is `system` (`liquidation` or `take_profit_stop_loss`).

### July 4, 2026

- **REST (Predictions)**: `GET /exchange/announcements` removed. Use `GET /exchange/schedule`.

### July 2, 2026

- **REST, FIX (Predictions)**: Sub-account-restricted API keys. Pass `subaccount` (0-63) to `POST /api_keys` or `POST /api_keys/generate`.
- **REST (Predictions)**: Per-index exchange status. `GET /exchange/status` returns `exchange_index_statuses` + `intra_exchange_transfers_active`.
- **REST (Predictions)**: Per-index subaccount balances. `GET /portfolio/subaccounts/balances` returns balance per exchange index.
- **REST (Margin)**: `is_portfolio` flag on margin positions. Per-position risk metrics omitted when jointly margined.
- **WebSocket (Predictions)**: `price_ranges` added to `market_lifecycle_v2` events (`created`, `price_level_structure_updated`).
- **REST (Predictions)**: Multivariate lookup history endpoints fully deprecated.

### June 30, 2026

- **REST (Predictions, Margin)**: `write::trade` API key permission for order/RFQ write access without transfer-write access.

### June 25, 2026

- **REST, FIX (Predictions)**: RFQ quote retention — quotes only durably queryable after acceptance. RFQ-scoped quote action endpoints.
- **REST (Predictions)**: API usage tier qualification requirements halved.
- **FIX (Predictions)**: Exchange index routing via `ExDestination<100>`. `-1` for auto-route.
- **FIX (Predictions)**: RFQ quotes support post-only via `ExecInst<18>=6`.

### June 18, 2026

- **REST (Predictions)**: Legacy `/portfolio/orders` mutation endpoints deprecated. Use V2 `/portfolio/events/orders` endpoints.
- **REST (Predictions)**: `tickers` query parameter on `GET /events` for comma-separated event ticker filter.
- **REST (Predictions)**: `settlement_sources` added to events API.
- **WebSocket (Predictions)**: `strike_type` and `cap_strike` on `market_lifecycle_v2` `metadata_updated` events.
- **FIX (Predictions, Margin)**: Trade entries in FIX market data incremental refreshes (`MDEntryType<269>=2`).
- **WebSocket (Predictions)**: Sanity limits on orderbook subscriptions: max 500k market subscriptions per session, max 10k commands/s.

### June 11, 2026

- **REST (Predictions, Margin)**: Automated API rate-limit tiers. Premier, Paragon, Prime earned from trading volume. `grants` array on `GET /account/limits`.
- **REST (Predictions, Margin)**: Self-serve Advanced API tier upgrade via `POST /account/api_usage_level/upgrade`.
- **REST (Predictions, Margin)**: API usage volume progress endpoint.
- **REST (Margin)**: Mark prices on margin market responses.
- **REST (Margin)**: `GET /margin/fee_tiers` returns active maker/taker fee rates.
- **REST, WebSocket (Margin)**: Dollar notional companions for volume, 24h volume, open interest.
- **REST (Margin)**: `tick_size` added to margin market responses.
- **REST, FIX (Predictions)**: Fractional quantities for RFQs (`contracts_fp` in 0.01 increments).

### June 4, 2026

- **REST (Predictions)**: Legacy order mutation rate-limit costs increased to 10x V2.
- **REST, WebSocket (Predictions)**: `PostOnlyCrossCancel` update reason.
- **FIX (Predictions)**: v1.0.31 — `POST_ONLY_CROSS` cancel reason on ExecutionReports.
- **FIX (Predictions)**: v1.0.30 — `EXCHANGE_UNAVAILABLE` vs `INTERNAL_ERROR` reject distinction.

### June 2, 2026

- **REST (Predictions, Margin)**: `write::transfer` API key permission for transfer-scoped write access.

### May 29, 2026

- **REST (Predictions)**: Block trade indicators (`is_block_trade`) on public trade endpoints.
- **FIX (Predictions)**: v1.0.29 — Security Status market lifecycle on KalshiMD.

### May 28, 2026

- **REST (Predictions)**: `balance_dollars` on `GET /portfolio/balance` (fixed-point dollar string, centi-cent precision for direct members).
- **FIX (Predictions)**: v1.0.28 — Market data on dedicated KalshiMD session.
- **FIX (Predictions)**: v1.0.27 — Four-decimal BALANCE collateral changes for direct members.

### May 25, 2026

- **REST (Predictions)**: Legacy order mutation rate-limit costs increased (first step toward 10x).

## FIX API Versions

| Version | Date | Key Changes |
| ------- | ---- | ----------- |
| v1.0.31 | Jun 4, 2026 | `POST_ONLY_CROSS` cancel reason |
| v1.0.30 | Jun 4, 2026 | `EXCHANGE_UNAVAILABLE` vs `INTERNAL_ERROR` reject distinction |
| v1.0.29 | May 29, 2026 | SecurityStatus market lifecycle on KalshiMD |
| v1.0.28 | May 28, 2026 | Market data on KalshiMD (MarketDataRequest/Snapshot/Incremental) |
| v1.0.27 | May 28, 2026 | Four-decimal BALANCE for direct members |

## OpenAPI Spec Versions

| Version | Date |
| ------- | ---- |
| 3.25.0  | Current (July 2026) |
| 3.24.0  | Previous |
