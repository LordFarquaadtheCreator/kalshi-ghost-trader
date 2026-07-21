# Kalshi REST API Reference

> Complete endpoint inventory from OpenAPI spec v3.25.0

Base URLs:
- Production: `https://external-api.kalshi.com/trade-api/v2`
- Production (legacy): `https://api.elections.kalshi.com/trade-api/v2`
- Demo: `https://external-api.demo.kalshi.co/trade-api/v2`
- Demo (legacy): `https://demo-api.kalshi.co/trade-api/v2`

Authentication: RSA-PSS-SHA256 signed headers (see [API Keys](gs_api_keys.md))

Pagination: Cursor-based. `limit` (max 200), `cursor` from previous response.

## Exchange

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/exchange/status` | GetExchangeStatus | Get Exchange Status |
| GET | `/exchange/schedule` | GetExchangeSchedule | Get Exchange Schedule |
| GET | `/exchange/user_data_timestamp` | GetUserDataTimestamp | Get User Data Timestamp |
| GET | `/series/fee_changes` | GetSeriesFeeChanges | Get Series Fee Changes |

## Markets

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/markets` | GetMarkets | Get Markets (filter by status, series_ticker, event_ticker) |
| GET | `/markets/{ticker}` | GetMarket | Get Market by ticker |
| GET | `/markets/{ticker}/orderbook` | GetMarketOrderbook | Get Market Orderbook |
| GET | `/markets/orderbooks` | GetMarketOrderbooks | Get Multiple Market Orderbooks |
| GET | `/markets/trades` | GetTrades | Get Trades (all markets, paginated) |
| GET | `/markets/candlesticks` | GetMarketCandlesticks | Get Market Candlesticks |
| GET | `/markets/candlesticks/batch` | BatchGetMarketCandlesticks | Batch Get Market Candlesticks |
| GET | `/series/{series_ticker}/markets/{ticker}/candlesticks` | GetMarketCandlesticks | Get Market Candlesticks (path variant) |
| GET | `/series/{series_ticker}/events/{ticker}/candlesticks` | GetMarketCandlesticksByEvent | Get Event Candlesticks |

## Series

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/series` | GetSeriesList | Get Series List |
| GET | `/series/{series_ticker}` | GetSeries | Get Series by ticker |

## Events

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/events` | GetEvents | Get Events (excludes multivariate) |
| GET | `/events/multivariate` | GetMultivariateEvents | Get Multivariate Events |
| GET | `/events/{event_ticker}` | GetEvent | Get Event by ticker |
| GET | `/events/{event_ticker}/metadata` | GetEventMetadata | Get Event Metadata |
| GET | `/events/fee_changes` | GetEventFeeChanges | Get Event Fee Changes |
| GET | `/series/{series_ticker}/events/{ticker}/forecast_percentile_history` | GetEventForecastPercentilesHistory | Get Event Forecast Percentile History |

## Orders (V2)

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| POST | `/portfolio/events/orders` | CreateOrderV2 | Create Order (V2) |
| POST | `/portfolio/events/orders/batched` | BatchCreateOrdersV2 | Batch Create Orders (V2) |
| DELETE | `/portfolio/events/orders/batched` | BatchCancelOrdersV2 | Batch Cancel Orders (V2) |
| DELETE | `/portfolio/events/orders/{order_id}` | CancelOrderV2 | Cancel Order (V2) |
| POST | `/portfolio/events/orders/{order_id}/amend` | AmendOrderV2 | Amend Order (V2) |
| POST | `/portfolio/events/orders/{order_id}/decrease` | DecreaseOrderV2 | Decrease Order (V2) |

## Orders (V1 / Legacy)

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/portfolio/orders` | GetOrders | Get Orders |
| GET | `/portfolio/orders/{order_id}` | GetOrder | Get Order |
| GET | `/portfolio/orders/queue_positions` | GetOrderQueuePositions | Get Queue Positions for Orders |
| GET | `/portfolio/orders/{order_id}/queue_position` | GetOrderQueuePosition | Get Order Queue Position |

## Order Groups

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/portfolio/order_groups` | GetOrderGroups | Get Order Groups |
| POST | `/portfolio/order_groups/create` | CreateOrderGroup | Create Order Group |
| GET | `/portfolio/order_groups/{order_group_id}` | GetOrderGroup | Get Order Group |
| DELETE | `/portfolio/order_groups/{order_group_id}` | DeleteOrderGroup | Delete Order Group |
| POST | `/portfolio/order_groups/{order_group_id}/reset` | ResetOrderGroup | Reset Order Group |
| POST | `/portfolio/order_groups/{order_group_id}/trigger` | TriggerOrderGroup | Trigger Order Group |
| PUT | `/portfolio/order_groups/{order_group_id}/limit` | UpdateOrderGroupLimit | Update Order Group Limit |

## Portfolio

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/portfolio/balance` | GetBalance | Get Balance (cents) |
| GET | `/portfolio/positions` | GetPositions | Get Positions |
| GET | `/portfolio/fills` | GetFills | Get Fills |
| GET | `/portfolio/settlements` | GetSettlements | Get Settlements |
| GET | `/portfolio/deposits` | GetDeposits | Get Deposits |
| GET | `/portfolio/withdrawals` | GetWithdrawals | Get Withdrawals |
| GET | `/portfolio/summary/total_resting_order_value` | GetPortfolioRestingOrderTotalValue | Get Total Resting Order Value |
| POST | `/portfolio/intra_exchange_instance_transfer` | IntraExchangeInstanceTransfer | Intra Account Transfer |

## Subaccounts

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| POST | `/portfolio/subaccounts` | CreateSubaccount | Create Subaccount |
| POST | `/portfolio/subaccounts/transfer` | ApplySubaccountTransfer | Transfer Between Subaccounts |
| POST | `/portfolio/subaccounts/positions/transfer` | ApplySubaccountPositionTransfer | Transfer Position Between Subaccounts |
| GET | `/portfolio/subaccounts/balances` | GetSubaccountBalances | Get All Subaccount Balances |
| GET | `/portfolio/subaccounts/transfers` | GetSubaccountTransfers | Get Subaccount Transfers |
| GET | `/portfolio/subaccounts/netting` | GetSubaccountNetting | Get Subaccount Netting |
| PUT | `/portfolio/subaccounts/netting` | UpdateSubaccountNetting | Update Subaccount Netting |

## Communications (RFQ / Block Trades)

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/communications/id` | GetCommunicationsID | Get Communications ID |
| GET | `/communications/rfqs` | GetRFQs | Get RFQs |
| POST | `/communications/rfqs` | CreateRFQ | Create RFQ |
| GET | `/communications/rfqs/{rfq_id}` | GetRFQ | Get RFQ |
| DELETE | `/communications/rfqs/{rfq_id}` | DeleteRFQ | Delete RFQ |
| GET | `/communications/rfqs/{rfq_id}/quotes/{quote_id}` | GetRFQQuote | Get RFQ Quote |
| DELETE | `/communications/rfqs/{rfq_id}/quotes/{quote_id}` | DeleteRFQQuote | Delete RFQ Quote |
| PUT | `/communications/rfqs/{rfq_id}/quotes/{quote_id}/accept` | AcceptRFQQuote | Accept RFQ Quote |
| PUT | `/communications/rfqs/{rfq_id}/quotes/{quote_id}/confirm` | ConfirmRFQQuote | Confirm RFQ Quote |
| GET | `/communications/quotes` | GetQuotes | Get Quotes (deprecated) |
| POST | `/communications/quotes` | CreateQuote | Create Quote |
| GET | `/communications/quotes/{quote_id}` | GetQuote | Get Quote (deprecated) |
| DELETE | `/communications/quotes/{quote_id}` | DeleteQuote | Delete Quote (deprecated) |
| PUT | `/communications/quotes/{quote_id}/accept` | AcceptQuote | Accept Quote (deprecated) |
| PUT | `/communications/quotes/{quote_id}/confirm` | ConfirmQuote | Confirm Quote (deprecated) |
| GET | `/communications/block-trade-proposals` | GetBlockTradeProposals | Get Block Trade Proposals |
| POST | `/communications/block-trade-proposals` | ProposeBlockTrade | Propose Block Trade |
| POST | `/communications/block-trade-proposals/{block_trade_proposal_id}/accept` | AcceptBlockTradeProposal | Accept Block Trade Proposal |

## API Keys

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/api_keys` | GetApiKeys | Get API Keys |
| POST | `/api_keys` | CreateApiKey | Create API Key (user-provided public key) |
| POST | `/api_keys/generate` | GenerateApiKey | Generate API Key (auto key pair) |
| DELETE | `/api_keys/{api_key}` | DeleteApiKey | Delete API Key |

## Account

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/account/limits` | GetAccountApiLimits | Get Account API Limits |
| POST | `/account/api_usage_level/upgrade` | UpgradeAccountApiUsageLevel | Upgrade Account API Usage Level |
| GET | `/account/api_usage_level/volume_progress` | GetAccountApiUsageLevelVolumeProgress | Get Account API Usage Level Volume Progress |
| GET | `/account/endpoint_costs` | GetAccountEndpointCosts | List Non-Default Endpoint Costs |

## Search

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/search/tags_by_categories` | GetTagsForSeriesCategories | Get Tags for Series Categories |
| GET | `/search/filters_by_sport` | GetFiltersForSports | Get Filters for Sports |

## Live Data

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/live_data/milestone/{milestone_id}` | GetLiveDataByMilestone | Get Live Data |
| GET | `/live_data/{type}/milestone/{milestone_id}` | GetLiveData | Get Live Data (legacy, with type) |
| POST | `/live_data/batch` | GetLiveDatas | Get Multiple Live Data |
| GET | `/live_data/milestone/{milestone_id}/game_stats` | GetGameStats | Get Game Stats |

## Milestones

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/milestones/{milestone_id}` | GetMilestone | Get Milestone |
| GET | `/milestones` | GetMilestones | Get Milestones |

## Structured Targets

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/structured_targets` | GetStructuredTargets | Get Structured Targets |
| GET | `/structured_targets/{structured_target_id}` | GetStructuredTarget | Get Structured Target |

## Multivariate Event Collections

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/multivariate_event_collections` | GetMultivariateEventCollections | Get Multivariate Event Collections |
| GET | `/multivariate_event_collections/{collection_ticker}` | GetMultivariateEventCollection | Get Multivariate Event Collection |
| POST | `/multivariate_event_collections/{collection_ticker}/create` | CreateMarketInMultivariateEventCollection | Create Market in MVE Collection |
| GET | `/multivariate_event_collections/{collection_ticker}/lookup` | LookupTickersForMarketInMultivariateEventCollection | Lookup Tickers (deprecated) |

## Incentive Programs

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/incentive_programs` | GetIncentivePrograms | Get Incentives |

## FCM

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/fcm/orders` | GetFCMOrders | Get FCM Orders |
| GET | `/fcm/positions` | GetFCMPositions | Get FCM Positions |

## Historical Data

| Method | Path | Operation | Summary |
|--------|------|-----------|---------|
| GET | `/historical/cutoff` | GetHistoricalCutoff | Get Historical Cutoff Timestamps |
| GET | `/historical/markets` | GetHistoricalMarkets | Get Historical Markets |
| GET | `/historical/markets/{ticker}` | GetHistoricalMarket | Get Historical Market |
| GET | `/historical/markets/{ticker}/candlesticks` | GetMarketCandlesticksHistorical | Get Historical Market Candlesticks |
| GET | `/historical/orders` | GetHistoricalOrders | Get Historical Orders |
| GET | `/historical/fills` | GetFillsHistorical | Get Historical Fills |
| GET | `/historical/trades` | GetTradesHistorical | Get Historical Trades |

## Key Schemas

### Market Object

Key fields on market responses:

- `ticker` — unique market identifier
- `event_ticker` — parent event
- `title`, `subtitle`, `yes_sub_title`, `no_sub_title`
- `status` — `unopened`, `open`, `closed`, `settled`
- `open_time`, `close_time`, `expected_expiration_time`, `expiration_time`
- `yes_bid_dollars`, `yes_ask_dollars`, `yes_bid_size_fp`, `yes_ask_size_fp`
- `no_bid_dollars`, `no_ask_dollars`
- `last_price_dollars`, `previous_price_dollars`
- `volume_fp`, `volume_24h_fp`, `open_interest_fp`
- `liquidity_dollars`, `notional_value_dollars`
- `price_level_structure` — `linear_cent`, `tapered_deci_cent`, `deci_cent`
- `price_ranges` — array of `{start, end, step}` defining valid price intervals
- `can_close_early`, `early_close_condition`
- `floor_strike`, `cap_strike`, `functional_strike`, `custom_strike`
- `settlement_value_dollars`, `settlement_ts`
- `occurrence_datetime` — when the real-world event occurs
- `rules_primary`, `rules_secondary`
- `exchange_index` — exchange shard
- `is_provisional`
- `primary_participant_key`

### Event Object

- `event_ticker`, `series_ticker`, `title`, `sub_title`
- `category`, `mutually_exclusive`, `collateral_return_type`
- `strike_date`, `strike_period`
- `settlement_sources` — array of `{name, url}`
- `markets` — nested market objects (when `with_nested_markets=true`)
- `fee_type_override`, `fee_multiplier_override`
- `exchange_index`
- `product_metadata`
- `last_updated_ts`

### Order Object (V2)

Create order request fields:
- `ticker` — market ticker
- `side` — `bid` or `ask`
- `count` / `count_fp` — contract count (integer or fixed-point string)
- `price` / `price_dollars` — price (cents or dollar string)
- `time_in_force` — `good_till_canceled`, `immediate_or_cancel`, `fill_or_kill`
- `self_trade_prevention_type` — `taker_at_cross`, `maker_at_cross`, `cancel_newest`, `cancel_oldest`
- `client_order_id` — dedup UUID
- `cancel_order_on_pause` — boolean
- `order_group_id` — optional order group

### Exchange Status

- `exchange_active` — boolean, false during maintenance
- `trading_active` — boolean, false outside trading hours
- `intra_exchange_transfers_active` — boolean
- `exchange_estimated_resume_time` — datetime or null
- `exchange_index_statuses` — per-shard status array

## Rate Limits

Token-based budget system. Tiers: Basic, Advanced, Expert, Premier, Paragon, Prime, Prestige.

See [Rate Limits](gs_rate_limits.md) for full details.
