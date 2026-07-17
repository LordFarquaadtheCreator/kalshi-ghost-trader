<script>
  import LineChart from '$lib/components/LineChart.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { systemStore as store } from '$lib/system-store.js';
  import { fmtBytes, fmtNum } from '$lib/utils.js';

  /** @type {any[]} */
  const cpuSeries = [
    { label: 'Heap (MB)', getValue: (/** @type {any} */ s) => Math.round((s.heap_alloc_bytes / 1048576) * 10) / 10, color: '#60a5fa' },
  ];

  /** @type {any[]} */
  const rssSeries = [
    { label: 'Heap (MB)', getValue: (/** @type {any} */ s) => Math.round((s.heap_alloc_bytes / 1048576) * 10) / 10, color: '#60a5fa' },
    { label: 'Heap Sys (MB)', getValue: (/** @type {any} */ s) => Math.round((s.heap_sys_bytes / 1048576) * 10) / 10, color: '#a78bfa' },
    { label: 'Stack (MB)', getValue: (/** @type {any} */ s) => Math.round((s.stack_inuse_bytes / 1048576) * 10) / 10, color: '#f472b0' },
    { label: 'Total Sys (MB)', getValue: (/** @type {any} */ s) => Math.round((s.sys_bytes / 1048576) * 10) / 10, color: '#fbbf24' },
  ];

  /** @type {any[]} */
  const goroutineSeries = [{ label: 'Goroutines', getValue: (/** @type {any} */ s) => s.goroutines, color: '#34d399' }];
  /** @type {any[]} */
  const gcSeries = [{ label: 'GC Count', getValue: (/** @type {any} */ s) => s.gc_num, color: '#fbbf24' }];
  /** @type {any[]} */
  const gcPauseSeries = [{ label: 'GC Pause (ms)', getValue: (/** @type {any} */ s) => Math.round((s.gc_pause_ns / 1e6) * 100) / 100, color: '#f87171' }];
  /** @type {any[]} */
  const heapObjSeries = [{ label: 'Heap Objects', getValue: (/** @type {any} */ s) => s.heap_objects, color: '#60a5fa' }];
  /** @type {any[]} */
  const mallocFreesSeries = [
    { label: 'Mallocs', getValue: (/** @type {any} */ s) => s.mallocs, color: '#34d399' },
    { label: 'Frees', getValue: (/** @type {any} */ s) => s.frees, color: '#f87171' },
  ];
  /** @type {any[]} */
  const stackSeries = [{ label: 'Stack (MB)', getValue: (/** @type {any} */ s) => Math.round((s.stack_inuse_bytes / 1048576) * 10) / 10, color: '#f472b0' }];
</script>

<svelte:head>
  <title>System — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="System" connected={$store.connected} error={$store.error || ''} />

  {#if $store.current}
    <div class="stats-grid">
      <StatCard label="Goroutines" value={$store.current.goroutines} />
      <StatCard label="Heap" value={fmtBytes($store.current.heap_alloc_bytes)} />
      <StatCard label="Heap Objects" value={fmtNum($store.current.heap_objects)} />
      <StatCard label="Total Sys" value={fmtBytes($store.current.sys_bytes)} />
      <StatCard label="GC Count" value={$store.current.gc_num} />
      <StatCard label="GC Pause" value={`${($store.current.gc_pause_ns / 1e6).toFixed(2)} ms`} />
      <StatCard label="Mallocs" value={fmtNum($store.current.mallocs)} />
      <StatCard label="Frees" value={fmtNum($store.current.frees)} />
      <StatCard label="CPUs" value={$store.current.num_cpu} />
      <StatCard label="Next GC" value={fmtBytes($store.current.next_gc_bytes)} />
    </div>
  {:else}
    <div class="stats-grid">
      <div class="stat-card" style="grid-column: 1 / -1; text-align: center; padding: 30px;">
        <div class="stat-label">Waiting for data...</div>
      </div>
    </div>
  {/if}

  <div class="charts-grid">
    <LineChart title="Heap Allocation (MB)" series={cpuSeries} {store} yUnit=" MB" />
    <LineChart title="Memory Breakdown (MB)" series={rssSeries} {store} yUnit=" MB" />
    <LineChart title="Goroutines" series={goroutineSeries} {store} />
    <LineChart title="Heap Objects" series={heapObjSeries} {store} />
    <LineChart title="GC Count" series={gcSeries} {store} />
    <LineChart title="GC Pause Total (ms)" series={gcPauseSeries} {store} yUnit=" ms" />
    <LineChart title="Mallocs vs Frees" series={mallocFreesSeries} {store} />
    <LineChart title="Stack In-Use (MB)" series={stackSeries} {store} yUnit=" MB" />
  </div>
</div>
