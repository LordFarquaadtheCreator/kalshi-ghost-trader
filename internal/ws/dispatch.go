package ws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
)

// wsEnvelope is the outer wrapper of every Kalshi WS message.
type wsEnvelope struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	SID  int64           `json:"sid"`
	Seq  int64           `json:"seq"`
	Msg  json.RawMessage `json:"msg"`
}

func (m *Manager) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		m.handleMessage(data)
	}
}

// handleMessage parses a WS message and routes it to the appropriate handler.
func (m *Manager) handleMessage(data []byte) {
	var env wsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return // skip malformed
	}

	switch env.Type {
	case "subscribed":
		m.handleSubscribed(env.ID, env.Msg)
	case "ok":
		// Server returns ok instead of subscribed when markets are auto-merged
		// into an existing subscription (same channels). Treat as ack.
		m.handleOk(env.ID, env.Msg)
	case "unsubscribed":
		// Ack — nothing to do, sub already removed from map
	case "error":
		m.handleWsError(env)
	case "ticker":
		m.trackSeq(env.SID, env.Seq)
		m.handleTicker(env.SID, env.Msg, data)
	case "trade":
		m.trackSeq(env.SID, env.Seq)
		m.handleTrade(env.SID, env.Msg, data)
	case "orderbook_snapshot":
		m.trackSeq(env.SID, env.Seq)
		m.handleOrderbookSnapshot(env.SID, env.Seq, env.Msg, data)
	case "orderbook_delta":
		m.trackSeq(env.SID, env.Seq)
		m.handleOrderbookDelta(env.SID, env.Seq, env.Msg, data)
	case "market_lifecycle_v2":
		m.handleLifecycle(env.Msg, data)
	case "event_lifecycle":
		m.handleEventLifecycle(env.Msg, data)
	case "event_fee_update":
		// Part of market_lifecycle_v2 channel — skip
	default:
		// Unknown type — skip
	}
}

// trackSeq detects gaps in per-sid message sequence numbers. Kalshi assigns
// monotonically increasing seq values per sid. A gap means messages were lost
// between server and client (network drop, slow consumer, etc).
func (m *Manager) trackSeq(sid, seq int64) {
	if sid == 0 || seq == 0 {
		return
	}
	m.seqMu.Lock()
	prev, exists := m.lastSeq[sid]
	if exists && seq > prev+1 {
		missed := seq - prev - 1
		m.SeqGaps.Add(missed)
		m.log.Warn("ws seq gap", "sid", sid, "prev", prev, "cur", seq, "missed", missed, "total_gaps", m.SeqGaps.Load())
	}
	m.lastSeq[sid] = seq
	m.seqMu.Unlock()
}

// trackLatency records recv_ts - server_ts for ticker/trade messages.
// Exposed via metrics to detect WS transport delays.
func (m *Manager) trackLatency(recvTs, serverTs int64) {
	latency := recvTs - serverTs
	if latency < 0 {
		return // clock skew — ignore
	}
	m.latencyMu.Lock()
	m.latencySum += latency
	m.latencyCount++
	if latency > m.latencyMax {
		m.latencyMax = latency
	}
	m.latencyMu.Unlock()
}

// LatencyStats returns avg/max latency in ms and sample count.
func (m *Manager) LatencyStats() (avg, max, count int64) {
	m.latencyMu.Lock()
	defer m.latencyMu.Unlock()
	if m.latencyCount > 0 {
		avg = m.latencySum / m.latencyCount
	}
	return avg, m.latencyMax, m.latencyCount
}

// handleWsError logs WS errors and propagates subscribe failures to
// any pending Subscribe caller waiting on ack.
func (m *Manager) handleWsError(env wsEnvelope) {
	var errMsg struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(env.Msg, &errMsg); err == nil {
		m.log.Warn("ws error", "code", errMsg.Code, "msg", errMsg.Msg)
	}

	m.cmdMu.Lock()
	market, ok := m.cmdToMarket[env.ID]
	m.cmdMu.Unlock()
	if !ok {
		return
	}
	m.mu.Lock()
	if info, exists := m.subs[market]; exists {
		select {
		case <-info.acked:
		default:
			info.ackErr = fmt.Errorf("server error %d: %s", errMsg.Code, errMsg.Msg)
			close(info.acked)
		}
	}
	m.mu.Unlock()
}

// handleOk handles ok responses. Server returns ok instead of subscribed
// when a subscribe command's channels already have an active subscription —
// the new markets are auto-merged into the existing sids. We treat this as
// a successful ack for the pending Subscribe caller.
func (m *Manager) handleOk(cmdID int64, msg json.RawMessage) {
	m.cmdMu.Lock()
	market, ok := m.cmdToMarket[cmdID]
	m.cmdMu.Unlock()
	if !ok {
		return // not a per-market subscribe
	}
	m.mu.Lock()
	if info, exists := m.subs[market]; exists {
		select {
		case <-info.acked:
		default:
			close(info.acked)
		}
	}
	m.mu.Unlock()
}

// handleSubscribed maps the server-assigned sid back to the market
// using the command id we tracked when sending the subscribe command.
// Kalshi sends one "subscribed" per channel (ticker, trade), each with
// its own sid. Both sids are stored against the market. First ack
// unblocks the pending Subscribe caller.
func (m *Manager) handleSubscribed(cmdID int64, msg json.RawMessage) {
	var resp struct {
		Channel string `json:"channel"`
		SID     int64  `json:"sid"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		return
	}

	m.cmdMu.Lock()
	market, ok := m.cmdToMarket[cmdID]
	m.cmdMu.Unlock()
	if !ok {
		if resp.Channel == "market_lifecycle_v2" {
			m.log.Info("subscribed to lifecycle", "sid", resp.SID)
		}
		return
	}
	m.mu.Lock()
	info, exists := m.subs[market]
	if exists {
		info.sids = append(info.sids, resp.SID)
		select {
		case <-info.acked:
		default:
			close(info.acked)
		}
	}
	m.mu.Unlock()
}
