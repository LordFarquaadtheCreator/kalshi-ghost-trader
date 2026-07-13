package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

// Subscribe registers interest in a market's ticker + trade updates.
// Waits for server ack (or error) before returning. On no connection,
// subscription is queued for replay on next connect.
func (m *Manager) Subscribe(ctx context.Context, market string) error {
	m.mu.Lock()
	if _, ok := m.subs[market]; ok {
		m.mu.Unlock()
		return nil // already subscribed
	}
	info := &subInfo{acked: make(chan struct{})}
	m.subs[market] = info
	conn := m.conn
	m.mu.Unlock()

	if conn == nil {
		return nil // replayed on connect
	}

	if err := m.sendSubscribeConn(ctx, conn, []string{market}); err != nil {
		m.removeSub(market)
		return fmt.Errorf("subscribe %s: %w", market, err)
	}

	// Wait for server ack or error
	select {
	case <-info.acked:
		if info.ackErr != nil {
			m.removeSub(market)
			return fmt.Errorf("subscribe %s: %w", market, info.ackErr)
		}
	case <-time.After(subscribeAckTimeout):
		m.removeSub(market)
		return fmt.Errorf("subscribe %s: ack timeout", market)
	case <-ctx.Done():
		m.removeSub(market)
		return ctx.Err()
	}
	return nil
}

// Unsubscribe removes interest in a market and sends a server-side
// unsubscribe command using the stored sids.
func (m *Manager) Unsubscribe(ctx context.Context, market string) {
	m.mu.Lock()
	info, ok := m.subs[market]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.subs, market)
	conn := m.conn
	m.mu.Unlock()

	if len(info.sids) > 0 && conn != nil {
		id := m.msgID.Add(1)
		unsub := map[string]any{
			"id":  id,
			"cmd": "unsubscribe",
			"params": map[string]any{
				"sids": info.sids,
			},
		}
		data, err := json.Marshal(unsub)
		if err != nil {
			m.log.Warn("marshal unsubscribe", "market", market, "err", err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			m.log.Warn("send unsubscribe", "market", market, "err", err)
		}
	}
}

func (m *Manager) removeSub(market string) {
	m.mu.Lock()
	delete(m.subs, market)
	m.mu.Unlock()
}

// replaySubscriptions resubscribes to all markets + lifecycle channel
// after a (re)connect. Per-market to preserve individual sids for unsubscribe.
func (m *Manager) replaySubscriptions(ctx context.Context) error {
	m.mu.Lock()
	markets := make([]string, 0, len(m.subs))
	for k := range m.subs {
		markets = append(markets, k)
		m.subs[k].sids = nil
		m.subs[k].acked = make(chan struct{})
		m.subs[k].ackErr = nil
	}
	conn := m.conn
	m.mu.Unlock()

	if conn == nil {
		return nil
	}

	if err := m.sendLifecycleSubscribe(ctx, conn); err != nil {
		return err
	}

	if len(markets) == 0 {
		return nil
	}

	m.log.Info("replaying subscriptions", "count", len(markets))

	for _, mk := range markets {
		if err := m.sendSubscribeConn(ctx, conn, []string{mk}); err != nil {
			return fmt.Errorf("replay subscribe %s: %w", mk, err)
		}
	}
	return nil
}

// sendSubscribeConn sends a subscribe command for ticker + trade on the
// given conn. Records the command id -> market mapping for sid tracking.
// Does NOT acquire m.mu — caller manages locking.
func (m *Manager) sendSubscribeConn(ctx context.Context, conn *websocket.Conn, markets []string) error {
	id := m.msgID.Add(1)
	if len(markets) == 1 {
		m.cmdMu.Lock()
		m.cmdToMarket[id] = markets[0]
		m.cmdMu.Unlock()
	}
	sub := map[string]any{
		"id":  id,
		"cmd": "subscribe",
		"params": map[string]any{
			"channels":       []string{"ticker", "trade"},
			"market_tickers": markets,
		},
	}
	data, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// sendLifecycleSubscribe subscribes to market_lifecycle_v2 (unfiltered).
// This channel does NOT support market_ticker filters. We get ALL lifecycle
// events and filter client-side. Also delivers event_lifecycle and
// event_fee_update messages.
func (m *Manager) sendLifecycleSubscribe(ctx context.Context, conn *websocket.Conn) error {
	id := m.msgID.Add(1)
	lcSub := map[string]any{
		"id":  id,
		"cmd": "subscribe",
		"params": map[string]any{
			"channels": []string{"market_lifecycle_v2"},
		},
	}
	lcData, err := json.Marshal(lcSub)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, lcData)
}
