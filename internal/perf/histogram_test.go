package perf

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestSnapshotEmpty(t *testing.T) {
	h := New("test")
	p50, p99, count := h.Snapshot()
	if p50 != 0 || p99 != 0 || count != 0 {
		t.Fatalf("empty histogram: got p50=%v p99=%v count=%v", p50, p99, count)
	}
}

func TestSnapshotPercentiles(t *testing.T) {
	h := New("test")
	for i := 1.0; i <= 100; i++ {
		h.Record(i)
	}
	p50, p99, count := h.Snapshot()
	if count != 100 {
		t.Fatalf("count: got %d want 100", count)
	}
	if p50 != 50 {
		t.Fatalf("p50: got %v want 50", p50)
	}
	if p99 != 99 {
		t.Fatalf("p99: got %v want 99", p99)
	}
}

func TestRingBufferOverflow(t *testing.T) {
	h := New("test")
	for i := 1.0; i <= windowSize+50; i++ {
		h.Record(i)
	}
	_, _, count := h.Snapshot()
	if count != int64(windowSize+50) {
		t.Fatalf("count: got %d want %d", count, windowSize+50)
	}
	p50, _, _ := h.Snapshot()
	if p50 < 1 || p50 > float64(windowSize+50) {
		t.Fatalf("p50 out of range: %v", p50)
	}
}

func TestRunLogsAndStops(t *testing.T) {
	h := New("test")
	h.Record(1.0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	h.Run(ctx, slog.Default(), 10*time.Millisecond)
}
