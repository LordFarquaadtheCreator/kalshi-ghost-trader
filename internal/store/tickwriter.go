package store

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	// tickChanBuffer sizes the tick ingest channel. Large to absorb WS bursts.
	tickChanBuffer = 8192
	// lifecycleChanBuffer sizes the lifecycle event ingest channel.
	lifecycleChanBuffer = 1024
	// eventLifecycleChanBuffer sizes the event lifecycle ingest channel.
	eventLifecycleChanBuffer = 1024
	// orderbookChanBuffer sizes the orderbook event ingest channel.
	// Deltas can be high-frequency during active trading.
	orderbookChanBuffer = 8192
	// ordersChanBuffer sizes the signal order ingest channel.
	// Low frequency — only fires on match points.
	ordersChanBuffer = 256
	// pointsChanBuffer sizes the point-by-point ingest channel.
	// Low frequency — one point per ~30s per match.
	pointsChanBuffer = 256
)

// TickWriter is the single writer goroutine that batches tick inserts.
// All ingest calls go through a channel; the writer drains in batches.
type TickWriter struct {
	db               *DB
	in               chan Tick
	lifecycleIn      chan LifecycleEvent
	eventLifecycleIn chan EventLifecycleEvent
	orderbookIn      chan OrderbookEvent
	ordersIn         chan Order
	pointsIn         chan Point
	batchSize        int
	flushTimeout     time.Duration
	log              *slog.Logger

	TickDrops         atomic.Int64
	OrderbookDrops    atomic.Int64
	LifecycleDrops    atomic.Int64
	EvtLifecycleDrops atomic.Int64
	OrdersDrops       atomic.Int64
	PointsDrops       atomic.Int64
}

// NewTickWriter creates a batched tick writer.
func (d *DB) NewTickWriter(batchSize, flushTimeoutMS int, log *slog.Logger) *TickWriter {
	return &TickWriter{
		db:               d,
		in:               make(chan Tick, tickChanBuffer),
		lifecycleIn:      make(chan LifecycleEvent, lifecycleChanBuffer),
		eventLifecycleIn: make(chan EventLifecycleEvent, eventLifecycleChanBuffer),
		orderbookIn:      make(chan OrderbookEvent, orderbookChanBuffer),
		ordersIn:         make(chan Order, ordersChanBuffer),
		pointsIn:         make(chan Point, pointsChanBuffer),
		batchSize:        batchSize,
		flushTimeout:     time.Duration(flushTimeoutMS) * time.Millisecond,
		log:              log,
	}
}

// Ingest enqueues a tick for batched write. Non-blocking; drops on full buffer.
func (w *TickWriter) Ingest(t Tick) {
	select {
	case w.in <- t:
	default:
		w.TickDrops.Add(1)
		w.log.Warn("tick buffer full, dropping", "market", t.MarketTicker, "total_drops", w.TickDrops.Load())
	}
}

// IngestLifecycle enqueues a lifecycle event for write.
func (w *TickWriter) IngestLifecycle(le LifecycleEvent) {
	select {
	case w.lifecycleIn <- le:
	default:
		w.LifecycleDrops.Add(1)
		w.log.Warn("lifecycle buffer full, dropping", "market", le.MarketTicker, "total_drops", w.LifecycleDrops.Load())
	}
}

// IngestEventLifecycle enqueues an event_lifecycle message for write.
func (w *TickWriter) IngestEventLifecycle(el EventLifecycleEvent) {
	select {
	case w.eventLifecycleIn <- el:
	default:
		w.EvtLifecycleDrops.Add(1)
		w.log.Warn("event lifecycle buffer full, dropping", "event", el.EventTicker, "total_drops", w.EvtLifecycleDrops.Load())
	}
}

// IngestOrderbook enqueues an orderbook event for batched write.
func (w *TickWriter) IngestOrderbook(oe OrderbookEvent) {
	select {
	case w.orderbookIn <- oe:
	default:
		w.OrderbookDrops.Add(1)
		w.log.Warn("orderbook buffer full, dropping", "market", oe.MarketTicker, "total_drops", w.OrderbookDrops.Load())
	}
}

// IngestPoint enqueues a point-by-point score entry for batched write.
// Non-blocking; drops on full buffer.
func (w *TickWriter) IngestPoint(p Point) {
	select {
	case w.pointsIn <- p:
	default:
		w.PointsDrops.Add(1)
		w.log.Warn("points buffer full, dropping", "total_drops", w.PointsDrops.Load())
	}
}

// IngestOrder enqueues a simulated order for batched write.
// Non-blocking; drops on full buffer. Returns false if dropped.
func (w *TickWriter) IngestOrder(o Order) bool {
	select {
	case w.ordersIn <- o:
		return true
	default:
		w.OrdersDrops.Add(1)
		w.log.Warn("orders buffer full, dropping", "match", o.MatchTicker, "total_drops", w.OrdersDrops.Load())
		return false
	}
}

// Run is the writer goroutine. Cancel ctx to stop; flushes remainder.
//
// The flush cadence uses a plain time.Ticker (no Stop/Reset choreography —
// the previous NewTimer + Stop/Reset pattern was a known Go footgun and the
// precision difference is irrelevant at these flush timeouts). The terminal
// flush on ctx cancellation uses a detached context (context.WithoutCancel
// with a 5s deadline) so the final batch lands even after the parent ctx is
// cancelled — the previous code wrote the final batch with an already-
// cancelled ctx, silently dropping it.
func (w *TickWriter) Run(ctx context.Context) error {
	batch := make([]Tick, 0, w.batchSize)
	obBatch := make([]OrderbookEvent, 0, w.batchSize)
	ordBatch := make([]Order, 0, 16)
	ptBatch := make([]Point, 0, 16)
	ticker := time.NewTicker(w.flushTimeout)
	defer ticker.Stop()

	flush := func(fctx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := w.db.InsertTickBatch(fctx, batch); err != nil {
			w.log.Error("write tick batch failed", "err", err, "n", len(batch))
		}
		batch = batch[:0]
	}

	flushOrderbook := func(fctx context.Context) {
		if len(obBatch) == 0 {
			return
		}
		if err := w.db.InsertOrderbookBatch(fctx, obBatch); err != nil {
			w.log.Error("write orderbook batch failed", "err", err, "n", len(obBatch))
		}
		obBatch = obBatch[:0]
	}

	flushOrders := func(fctx context.Context) {
		if len(ordBatch) == 0 {
			return
		}
		if err := w.db.InsertOrdersBatch(fctx, ordBatch); err != nil {
			w.log.Error("write orders batch failed", "err", err, "n", len(ordBatch))
		}
		ordBatch = ordBatch[:0]
	}

	flushPoints := func(fctx context.Context) {
		if len(ptBatch) == 0 {
			return
		}
		if err := w.db.InsertPointBatch(fctx, ptBatch); err != nil {
			w.log.Error("write points batch failed", "err", err, "n", len(ptBatch))
		}
		ptBatch = ptBatch[:0]
	}

	flushLifecycle := func(fctx context.Context) {
		for {
			select {
			case le := <-w.lifecycleIn:
				if err := w.db.InsertLifecycleEvent(fctx, le); err != nil {
					w.log.Error("write lifecycle failed", "err", err, "market", le.MarketTicker)
					continue
				}
				if err := w.db.ApplyLifecycleEvent(fctx, le); err != nil {
					w.log.Error("apply lifecycle failed", "err", err, "market", le.MarketTicker, "type", le.EventType)
				}
			default:
				return
			}
		}
	}

	flushEventLifecycle := func(fctx context.Context) {
		for {
			select {
			case el := <-w.eventLifecycleIn:
				if err := w.db.InsertEventLifecycleEvent(fctx, el); err != nil {
					w.log.Error("write event lifecycle failed", "err", err, "event", el.EventTicker)
				}
			default:
				return
			}
		}
	}

	flushAll := func(fctx context.Context) {
		flush(fctx)
		flushOrderbook(fctx)
		flushOrders(fctx)
		flushPoints(fctx)
		flushLifecycle(fctx)
		flushEventLifecycle(fctx)
	}

	for {
		select {
		case <-ctx.Done():
			// Drain in-flight ticks before flushing — buffered channel may
			// hold up to tickChanBuffer messages not yet in `batch`.
			for {
				select {
				case t := <-w.in:
					batch = append(batch, t)
				case oe := <-w.orderbookIn:
					obBatch = append(obBatch, oe)
				case o := <-w.ordersIn:
					ordBatch = append(ordBatch, o)
				case p := <-w.pointsIn:
					ptBatch = append(ptBatch, p)
				default:
					// Terminal flush: detached ctx so the final batch lands
					// even though the parent ctx is cancelled.
					flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					flushAll(flushCtx)
					cancel()
					return ctx.Err()
				}
			}

		case t := <-w.in:
			batch = append(batch, t)
			if len(batch) >= w.batchSize {
				flush(ctx)
			}

		case oe := <-w.orderbookIn:
			obBatch = append(obBatch, oe)
			if len(obBatch) >= w.batchSize {
				flushOrderbook(ctx)
			}

		case o := <-w.ordersIn:
			ordBatch = append(ordBatch, o)
			if len(ordBatch) >= 16 {
				flushOrders(ctx)
			}

		case p := <-w.pointsIn:
			ptBatch = append(ptBatch, p)
			if len(ptBatch) >= 16 {
				flushPoints(ctx)
			}

		case le := <-w.lifecycleIn:
			flush(ctx)
			if err := w.db.InsertLifecycleEvent(ctx, le); err != nil {
				w.log.Error("write lifecycle failed", "err", err, "market", le.MarketTicker)
			} else if err := w.db.ApplyLifecycleEvent(ctx, le); err != nil {
				w.log.Error("apply lifecycle failed", "err", err, "market", le.MarketTicker, "type", le.EventType)
			}

		case el := <-w.eventLifecycleIn:
			flush(ctx)
			if err := w.db.InsertEventLifecycleEvent(ctx, el); err != nil {
				w.log.Error("write event lifecycle failed", "err", err, "event", el.EventTicker)
			}

		case <-ticker.C:
			flushAll(ctx)
		}
	}
}
