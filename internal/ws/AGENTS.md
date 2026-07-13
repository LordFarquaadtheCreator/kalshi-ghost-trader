# internal/ws

WebSocket manager. Single multiplexed connection, auto-reconnect, per-market subscriptions.

## Files

- `manager.go` — Manager struct, Run, dial, reconnect, backoff
- `subscribe.go` — Subscribe, Unsubscribe, replaySubscriptions, sendSubscribeConn, sendLifecycleSubscribe
- `dispatch.go` — readLoop, handleMessage, handleWsError, handleSubscribed
- `messages.go` — handleTicker, handleTrade, handleLifecycle, handleEventLifecycle, message types
- `constants.go` — wsReadLimit, secondsToMillis, subscribeAckTimeout

## Channels subscribed

- `ticker` — market price updates. Filtered by market_tickers.
- `trade` — public trades. Filtered by market_tickers.
- `market_lifecycle_v2` — market status changes. NOT filterable. Gets ALL markets. Client-side filter by checking `m.subs`. Also delivers `event_lifecycle` and `event_fee_update` messages.

## Subscribe command

Two separate commands:
1. `{cmd: subscribe, channels: [ticker, trade], market_tickers: [...]}` — filtered
2. `{cmd: subscribe, channels: [market_lifecycle_v2]}` — unfiltered

Don't combine. `market_lifecycle_v2` doesn't support `market_ticker` params.

Replay subscribes per-market (not batched) so each market gets its own sids for clean unsubscribe.

## Subscribe ack

`Subscribe` waits for server ack (or error) before returning. Timeout: 5s. If server rejects (error message with matching cmd id), error propagated to caller. On no connection, returns nil — subscription queued for replay.

## Message types

- `subscribed` — subscribe ack. Carries `msg.channel` + `msg.sid`. Maps sid to market via `cmdToMarket` command id tracking. First ack unblocks pending Subscribe.
- `unsubscribed` — unsubscribe ack. Ignore.
- `ok` — update_subscription ack. Ignore.
- `error` — `{msg: {code, msg}}`. Log + propagate to pending Subscribe if cmd id matches.
- `ticker` — price update. Stored via tickWriter.
- `trade` — trade fill. Stored via tickWriter.
- `market_lifecycle_v2` — lifecycle event. Filtered by `m.subs` before storing.
- `event_lifecycle` — event creation notification. Filtered by `seriesFilter` (configured tennis series). Stored via `tickWriter.IngestEventLifecycle`.
- `event_fee_update` — fee override. Part of lifecycle channel. Skip.

## Sid tracking

`subInfo` per market holds `sids` (server-assigned subscription IDs) + `acked` channel + `ackErr`. `cmdToMarket` maps command id → market. When `subscribed` ack arrives, sid stored + first ack closes `acked` channel. `Unsubscribe` sends real unsubscribe command with stored sids.

## Timestamps

- `ticker.ts_ms` — milliseconds.
- `trade.ts_ms` — milliseconds.
- `lifecycle.open_ts`, `close_ts`, `determination_ts`, `settled_ts` — SECONDS. Multiply by 1000 before storing.

## Reconnect

Exponential backoff with jitter. Min from config, max 30s. On reconnect: clear `cmdToMarket`, reset sids + acked channels, replay all subscriptions per-market, resubscribe lifecycle channel.

## Locking

`m.mu` protects `conn` and `subs`. `cmdMu` protects `cmdToMarket`. `sendSubscribeConn` does NOT lock `m.mu` — caller manages. Locks `cmdMu` internally for `cmdToMarket` writes. `Subscribe` releases `m.mu` before waiting for ack — avoids blocking read loop during ack wait.

## Keepalive

`coder/websocket` auto-responds to ping frames during `Read()`. Kalshi sends ping every 10s with body `heartbeat`. No manual pong needed.

## Gotchas

- `event_fee_update` has `type: event_fee_update`, not `event_type`. Routed by `env.Type`.
- Lifecycle events arrive for ALL markets. Filter by `m.subs[market_ticker]` before storing.
- `event_lifecycle` arrives for ALL Kalshi events. Filter by `seriesFilter[series_ticker]` before storing.
- `conn.Write` is concurrent-safe in `coder/websocket`. Subscribe + replay can race.
- Read limit raised to 1MB. Default 32KB too small for ticker feeds.
- `cmdToMarket` must be cleared on reconnect — old command ids are invalid.
- No per-match dispatch channel. Ticks stored directly by WS manager via tickWriter. Tracker only manages subscription lifecycle.
