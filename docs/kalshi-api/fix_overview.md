# FIX API

> Financial Information eXchange protocol for high-performance trading on Kalshi

## Overview

Kalshi offers a FIX 5.0SP2 API for order entry, market data, drop copy, and RFQ functionality. The FIX API uses the same RSA key pairs as the REST API for authentication.

## Connectivity

### Production

- **Order Entry Host**: `mm.fix.elections.kalshi.com`
- **Market Data Host**: `marketdata.fix.elections.kalshi.com`

### Demo

- **Order Entry Host**: `fix.demo.kalshi.co`
- **Market Data Host**: `marketdata.fix.demo.kalshi.co`

### Session Types

| Purpose                              | Port | TargetCompID | Description                                                                                          |
| ------------------------------------ | ---- | ------------ | ---------------------------------------------------------------------------------------------------- |
| Order Entry (without retransmission) | 8228 | KalshiNR     | Submit, modify, cancel orders; no message persistence. Supports Listener Sessions.                  |
| Order Entry (with retransmission)    | 8230 | KalshiRT     | Order entry with retransmission, RFQ creation, settlement reports. Supports Listener Sessions.      |
| Drop Copy                            | 8229 | KalshiDC     | Request-response queries for historical execution reports.                                          |
| Post Trade                           | 8231 | KalshiPT     | Read-only stream for market settlement reports. Contact institutional@kalshi.com.                    |
| RFQ                                  | 8232 | KalshiRFQ    | Market maker session for RFQ broadcasts, quotes, quote lifecycle.                                    |
| Market Data                          | 8233 | KalshiMD     | Order book snapshots and incremental updates. Available only on market data host.                    |

### SSL/TLS

All FIX connections require TLS. Connect with `BeginString=FIXT.1.1`, `DefaultApplVerID=FIX50SP2<9>`.

### Rate Limits

FIX requests drain the same token buckets as REST. See [Rate Limits](gs_rate_limits.md).

### Maintenance Window

Thursday 3:00–5:00 AM ET. `CancelOrderOnPause` (tag 21006) controls whether orders auto-cancel during pauses.

## Authentication & Sessions

### API Key Setup

Same RSA key pair as REST. Generate 2048-bit RSA key, register public key in account profile. API Key ID (UUID) = `SenderCompID`.

### Logon (35=A)

Initiator sends Logon; acceptor responds with Logon (success) or Logout (failure).

**Required fields:**

| Tag  | Name             | Description                    | Value                    |
| ---- | ---------------- | ------------------------------ | ------------------------ |
| 98   | EncryptMethod    | Method of encryption           | None<0>                  |
| 96   | RawData          | Client logon message signature | Base64 encoded signature |
| 108  | HeartbeatInt     | Heartbeat interval (seconds)   | N > 3                    |
| 1137 | DefaultApplVerID | Default application version    | FIX50SP2<9>              |

**Optional fields:**

| Tag   | Name                     | Description                                                                 | Default           |
| ----- | ------------------------ | --------------------------------------------------------------------------- | ----------------- |
| 141   | ResetSeqNumFlag          | Reset seq numbers on logon. **Must be Y for non-retransmission sessions.**  | N                 |
| 8013  | CancelOrdersOnDisconnect | Cancel orders on any disconnection                                          | N                 |
| 20126 | ListenerSession          | Listen-only session. KalshiNR/RT only.                                      | N                 |
| 20127 | ReceiveSettlementReports | Receive settlement reports. KalshiRT/PT.                                    | N (Y on KalshiPT) |
| 20200 | MessageRetentionPeriod   | Retransmission retention, max 72h. KalshiRT/PT.                             | 24                |
| 21005 | UseDollars               | Enable dollar-based price format with subpenny precision                    | N                 |
| 21011 | SkipPendingExecReports   | Skip PENDING_{NEW|REPLACE|CANCEL} execution reports                         | N                 |
| 21012 | UseExpiredOrdStatus      | Emit Expired<C> for expiry-style cancellations instead of Canceled<4>       | N                 |
| 21007 | EnableIocCancelReport    | Partially filled IOC orders produce cancel report                           | N                 |
| 21008 | PreserveOriginalOrderQty | OrderQty tag 38 always reflects original order quantity                     | N                 |
| 21026 | AlwaysEmitNewBeforeTrade | Emit standalone New<0> before Trade<F> when both occur same cycle           | N                 |
| 21027 | SplitCollateralReturn    | Per-trade collateral return breakdown on Execution Reports                  | N                 |

### Signature Generation

`PreHashString = SendingTime + SOH + MsgType + SOH + MsgSeqNum + SOH + SenderCompID + SOH + TargetCompID`

SendingTime must match field 52 exactly. Format: `YYYYMMDD-HH:MM:SS.mmm`. Must be within 30 seconds of server time.

### Heartbeat & Sequence Numbers

- Default heartbeat: 30 seconds
- Missed heartbeat → connection terminates
- Lower seq than expected → connection terminated
- Higher seq than expected → recoverable with ResendRequest (KalshiRT/PT only)

### Message Retransmission

Only on KalshiRT and KalshiPT. For all other sessions, `ResetSeqNumFlag<141>` must be `Y`.

Drop Copy session provides alternative for querying missed execution reports.

## Order Entry

### New Order Single (35=D)

| Tag   | Name                    | Type         | Required | Description                                                                |
| ----- | ----------------------- | ------------ | -------- | -------------------------------------------------------------------------- |
| 11    | ClOrdID                 | String       | Y        | Client order ID, UUID preferred, max 64 chars                              |
| 18    | ExecInst                | Char         | N        | `6`=Post Only                                                              |
| 38    | OrderQty                | Decimal      | Y        | Quantity of contracts. Fractional supported.                               |
| 40    | OrdType                 | Char         | Y        | `2`=Limit                                                                 |
| 44    | Price                   | Integer      | Y        | Price per contract in cents (1–99). Use tag 21005 for dollar format.       |
| 54    | Side                    | Char         | Y        | `1`=Buy (Yes), `2`=Sell (No)                                               |
| 55    | Symbol                  | String       | Y        | Market ticker                                                              |
| 100   | ExDestination           | Integer      | N        | Exchange index. `-1` for auto-route.                                       |
| 59    | TimeInForce             | Char         | N        | `0`=Day, `1`=GTC, `3`=IOC, `4`=FOK, `6`=GTD                                |
| 126   | ExpireTime              | UTCTimestamp | C        | Required when TIF=GTD                                                      |
| 117   | QuoteId                 | UUID         | N        | Quote to accept for RFQ quote acceptance                                   |
| 79    | AllocAccount            | Integer      | N        | Subaccount number (0–63)                                                   |
| 526   | SecondaryClOrdID        | UUID         | N        | Order group identifier                                                     |
| 2964  | SelfTradePreventionType | Integer      | N        | `1`=Taker At Cross (default), `2`=Maker                                    |
| 21006 | CancelOrderOnPause      | Boolean      | N        | Cancel order if trading paused                                             |
| 21009 | MaxExecutionCost        | Decimal      | N        | Max execution cost in dollars                                              |
| 21023 | RfqId                   | UUID         | N        | Server-assigned RFQ ID for RFQ quote acceptance                            |

### Order Cancel/Replace Request (35=G)

Modifies existing order without canceling. Can change price and/or quantity.

### Order Cancel Request (35=F)

Cancels all remaining resting contracts. Returns canceled order details.

### Execution Report (35=8)

Sent for all order state changes. Key fields:

- `150` (ExecType): `0`=New, `1`=Partial fill, `2`=Fill, `4`=Canceled, `5`=Replace, `8`=Rejected, `F`=Trade
- `39` (OrdStatus): `0`=New, `1`=Partially filled, `2`=Filled, `4`=Canceled, `8`=Rejected, `C`=Expired
- `58` (Text): Rejection reason text
- `103` (OrdRejReason): Rejection reason code

### Mass Cancel Request (35=q) / Mass Cancel Report (35=r)

Cancel all open orders for a market or all markets.

## Market Data (KalshiMD)

### MarketDataRequest (35=V)

- `263=0`: Snapshot
- `263=1`: Snapshot + updates subscription
- `263=2`: Cancel subscription

### MarketDataSnapshotFullRefresh (35=W)

Full aggregated order book.

### MarketDataIncrementalRefresh (35=X)

Subsequent level changes. Trade entries: `MDEntryType<269>=2`.

### SecurityStatus (35=f)

Market lifecycle via `SecurityStatusRequest (35=e)`:
- `SecurityTradingStatus<326>`: `3`=resume (activated), `2`=trading halt, `100`=Kalshi determined, `101`=Kalshi settled
- Changes-only, no initial snapshot

## RFQ (KalshiRFQ)

Market maker session for:
- Receiving RFQ broadcasts (`QuoteRequest 35=R`)
- Submitting quotes (`Quote 35=S`)
- Managing quote lifecycle (`QuoteConfirm 35=U7`, `QuoteCancel 35=Z`)
- RFQ creators use `AcceptQuote (35=UA)` to accept

`RfqId<21023>` optional on quote actions, expected to become required.

## Subpenny Pricing

Enable with `UseDollars<21005>` on Logon. Prices in dollar format with subpenny precision instead of integer cents.

## FIX API Versions

Current: v1.0.31 (as of July 2026). See [Changelog](changelog.md) for version history.
