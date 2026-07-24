# /system — System Metrics

Go runtime metrics for ghost-trader. Polling runs constantly via singleton store.

## Files

- `+page.js` — SSR disabled. Initial load: `getMetrics()`.
- `+page.svelte` — Imports `systemStore` singleton from `$lib/system-store.js`. Polling starts in `+layout.svelte` on app load, persists across navigation.

## Data

- `systemStore` → `api.getMetrics()` → `GET :6060/metrics` → Go runtime MemStats + custom fields
- Fields: `goroutines`, `heap_alloc_bytes`, `heap_objects`, `sys_bytes`, `gc_num`, `gc_pause_ns`, `mallocs`, `frees`, `num_cpu`, `next_gc_bytes`, `stack_inuse_bytes`, `heap_sys_bytes`, `total_mem_bytes`, `cpu_usage_pct`
- 1s poll interval, 120 sample rolling window (2 min)

## UI

- StatCards: goroutines, heap, heap % RAM, CPU usage, GC rate, CPUs, total RAM, samples
- 9 LineCharts: heap allocation, memory breakdown, stack in-use, goroutines, heap objects, CPU usage, GC count, GC pause, mallocs vs frees

## Singleton Store

`systemStore` is module-level in `system-store.js`. Imported in `+layout.svelte` so polling starts immediately on app load. System page subscribes to same store — data available instantly on navigation, no loading gap.
