// Package perf provides minimal latency histograms for hot-path instrumentation.
// No external metrics dependency — p50/p99 are slog-exported on a timer.
package perf

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// windowSize caps stored samples. Enough for 60s of hot-path events
// at realistic rates without unbounded growth.
const windowSize = 8192

// Histogram is a fixed-size ring buffer of latency samples in milliseconds.
// Thread-safe. Snapshot copies and sorts the active window for percentiles.
type Histogram struct {
	mu      sync.Mutex
	samples [windowSize]float64
	head    int
	count   int64
	name    string
}

// New creates a named histogram. The name appears in slog output.
func New(name string) *Histogram {
	return &Histogram{name: name}
}

// Record adds a latency sample in milliseconds.
func (h *Histogram) Record(latencyMs float64) {
	h.mu.Lock()
	h.samples[h.head] = latencyMs
	h.head = (h.head + 1) % windowSize
	h.count++
	h.mu.Unlock()
}

// Snapshot returns p50, p99 (in ms), and total sample count.
// Copies the active window and sorts it — safe to call periodically.
func (h *Histogram) Snapshot() (p50, p99 float64, count int64) {
	h.mu.Lock()
	n := int(h.count)
	if n > windowSize {
		n = windowSize
	}
	buf := make([]float64, n)
	if h.count >= windowSize {
		copy(buf, h.samples[:])
	} else {
		copy(buf, h.samples[:n])
	}
	count = h.count
	h.mu.Unlock()

	if n == 0 {
		return 0, 0, 0
	}
	sort.Float64s(buf)
	p50 = percentile(buf, 0.50)
	p99 = percentile(buf, 0.99)
	return
}

// percentile picks the p-th quantile from a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// Run logs p50/p99 every interval until ctx is cancelled.
func (h *Histogram) Run(ctx context.Context, log *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p50, p99, count := h.Snapshot()
			log.Info("perf histogram",
				"name", h.name,
				"p50_ms", p50,
				"p99_ms", p99,
				"samples", count,
			)
		}
	}
}
