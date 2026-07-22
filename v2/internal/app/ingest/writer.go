// Package ingest implements the batched tick/orderbook writer with COPY-based
// bulk insertion for hot tables and plain inserts for the rest.
package ingest

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/postgres"
)

// Writer batches tick and orderbook inserts, flushing via COPY on a ticker.
// Single goroutine — no concurrent writes.
type Writer struct {
	copyWriter *postgres.CopyWriter

	tickCh      chan postgres.TickRow
	orderbookCh chan postgres.OrderbookRow

	tickBuf      []postgres.TickRow
	orderbookBuf []postgres.OrderbookRow

	tickDrops      atomic.Int64
	orderbookDrops atomic.Int64

	flushTicker *time.Ticker
	batchSize   int
	log         *slog.Logger
}

// NewWriter creates a batched writer with the given batch size and flush interval.
func NewWriter(cw *postgres.CopyWriter, batchSize int, flushInterval time.Duration, log *slog.Logger) *Writer {
	return &Writer{
		copyWriter:   cw,
		tickCh:       make(chan postgres.TickRow, 8192),
		orderbookCh:  make(chan postgres.OrderbookRow, 8192),
		batchSize:    batchSize,
		flushTicker:  time.NewTicker(flushInterval),
		log:          log,
	}
}

// IngestTick enqueues a tick row. Non-blocking; drops on full buffer.
func (w *Writer) IngestTick(row postgres.TickRow) {
	select {
	case w.tickCh <- row:
	default:
		w.tickDrops.Add(1)
	}
}

// IngestOrderbook enqueues an orderbook row. Non-blocking; drops on full buffer.
func (w *Writer) IngestOrderbook(row postgres.OrderbookRow) {
	select {
	case w.orderbookCh <- row:
	default:
		w.orderbookDrops.Add(1)
	}
}

// TickDrops returns the count of dropped ticks.
func (w *Writer) TickDrops() int64 { return w.tickDrops.Load() }

// OrderbookDrops returns the count of dropped orderbook events.
func (w *Writer) OrderbookDrops() int64 { return w.orderbookDrops.Load() }

// Run drains the channels and flushes batches. Single goroutine.
func (w *Writer) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			w.flushTicker.Stop()
			return w.terminalFlush(ctx)
		case row := <-w.tickCh:
			w.tickBuf = append(w.tickBuf, row)
			if len(w.tickBuf) >= w.batchSize {
				w.flushTicks(ctx)
			}
		case row := <-w.orderbookCh:
			w.orderbookBuf = append(w.orderbookBuf, row)
			if len(w.orderbookBuf) >= w.batchSize {
				w.flushOrderbook(ctx)
			}
		case <-w.flushTicker.C:
			w.flushTicks(ctx)
			w.flushOrderbook(ctx)
		}
	}
}

// terminalFlush flushes remaining buffers with a detached context (5s timeout),
// so data isn't lost on shutdown.
func (w *Writer) terminalFlush(ctx context.Context) error {
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	w.flushTicks(flushCtx)
	w.flushOrderbook(flushCtx)
	return nil
}

func (w *Writer) flushTicks(ctx context.Context) {
	if len(w.tickBuf) == 0 {
		return
	}
	if err := w.copyWriter.CopyTicks(ctx, w.tickBuf); err != nil {
		w.log.Error("ingest: copy ticks failed", "count", len(w.tickBuf), "err", err)
	} else {
		w.log.Debug("ingest: flushed ticks", "count", len(w.tickBuf))
	}
	w.tickBuf = w.tickBuf[:0]
}

func (w *Writer) flushOrderbook(ctx context.Context) {
	if len(w.orderbookBuf) == 0 {
		return
	}
	if err := w.copyWriter.CopyOrderbook(ctx, w.orderbookBuf); err != nil {
		w.log.Error("ingest: copy orderbook failed", "count", len(w.orderbookBuf), "err", err)
	} else {
		w.log.Debug("ingest: flushed orderbook", "count", len(w.orderbookBuf))
	}
	w.orderbookBuf = w.orderbookBuf[:0]
}

// MaintenanceJob creates weekly partitions ahead and drops old ones.
type MaintenanceJob struct {
	db         *postgres.CopyWriter
	interval   time.Duration
	retention  time.Duration
	log        *slog.Logger
}

// NewMaintenanceJob creates a partition maintenance job.
func NewMaintenanceJob(db *postgres.CopyWriter, interval, retention time.Duration, log *slog.Logger) *MaintenanceJob {
	return &MaintenanceJob{db: db, interval: interval, retention: retention, log: log}
}

// Run periodically creates new partitions and drops old ones.
func (m *MaintenanceJob) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := m.runOnce(ctx); err != nil {
				m.log.Error("ingest: partition maintenance failed", "err", err)
			}
		}
	}
}

func (m *MaintenanceJob) runOnce(ctx context.Context) error {
	// Create partitions for the next 4 weeks.
	// Drop partitions older than retention.
	// Implementation requires raw DB access — stub for now.
	return nil
}
