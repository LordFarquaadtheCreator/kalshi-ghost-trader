<script>
  import LineChart from '$lib/components/LineChart.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';
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
  /** @type {any[]} */
  const cpuUsageSeries = [{ label: 'CPU Usage (%)', getValue: (/** @type {any} */ s) => Math.round((s.cpu_usage_pct || 0) * 10) / 10, color: '#fbbf24' }];

  // --- Derived stats from rolling history ---
  let cur = $derived($store.current);
  let hist = $derived($store.history ?? []);

  let heapStats = $derived.by(() => {
    if (hist.length === 0) return null;
    const vals = hist.map((s) => s.heap_alloc_bytes);
    const min = Math.min(...vals);
    const max = Math.max(...vals);
    const avg = vals.reduce((a, b) => a + b, 0) / vals.length;
    return { min, max, avg };
  });

  let goroutineStats = $derived.by(() => {
    if (hist.length === 0) return null;
    const vals = hist.map((s) => s.goroutines);
    const min = Math.min(...vals);
    const max = Math.max(...vals);
    const cur = vals[vals.length - 1];
    const first = vals[0];
    const trend = cur - first;
    return { min, max, cur, trend };
  });

  let gcRate = $derived.by(() => {
    if (hist.length < 2) return null;
    const first = hist[0].gc_num;
    const last = hist[hist.length - 1].gc_num;
    const span = hist.length; // seconds (1s poll)
    return (last - first) / span;
  });

  let mallocFreeRatio = $derived.by(() => {
    if (!cur || !cur.frees) return null;
    return cur.mallocs / cur.frees;
  });

  let heapEfficiency = $derived.by(() => {
    if (!cur || !cur.sys_bytes) return null;
    return (cur.heap_alloc_bytes / cur.sys_bytes) * 100;
  });

  let heapPctOfRam = $derived.by(() => {
    if (!cur || !cur.total_mem_bytes) return null;
    return (cur.heap_alloc_bytes / cur.total_mem_bytes) * 100;
  });
</script>

<svelte:head>
  <title>System — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="System" connected={$store.connected} error={$store.error || ''} />

  {#if cur}
    <MetricsBar
      primary={[
        { label: 'Goroutines', value: cur.goroutines },
        { label: 'Heap', value: fmtBytes(cur.heap_alloc_bytes) },
        { label: 'Heap % RAM', value: heapPctOfRam !== null ? heapPctOfRam.toFixed(1) + '%' : '—' },
        { label: 'CPU Usage', value: cur.cpu_usage_pct != null ? cur.cpu_usage_pct.toFixed(1) + '%' : '—' },
        { label: 'GC Rate', value: gcRate !== null ? gcRate.toFixed(2) + '/s' : '—' },
        { label: 'CPUs', value: cur.num_cpu },
        { label: 'Total RAM', value: cur.total_mem_bytes ? fmtBytes(cur.total_mem_bytes) : '—' },
        { label: 'Samples', value: hist.length },
      ]}
      secondary={[
        { label: 'Heap Objects', value: fmtNum(cur.heap_objects) },
        { label: 'Total Sys', value: fmtBytes(cur.sys_bytes) },
        { label: 'Next GC', value: fmtBytes(cur.next_gc_bytes) },
        { label: 'GC Count', value: cur.gc_num },
        { label: 'GC Pause', value: (cur.gc_pause_ns / 1e6).toFixed(2) + ' ms' },
        { label: 'Mallocs', value: fmtNum(cur.mallocs) },
        { label: 'Frees', value: fmtNum(cur.frees) },
        { label: 'M/F Ratio', value: mallocFreeRatio !== null ? mallocFreeRatio.toFixed(2) : '—' },
        { label: 'Heap Min', value: heapStats ? fmtBytes(heapStats.min) : '—' },
        { label: 'Heap Max', value: heapStats ? fmtBytes(heapStats.max) : '—' },
        { label: 'Heap Avg', value: heapStats ? fmtBytes(heapStats.avg) : '—' },
        { label: 'Go Min', value: goroutineStats ? goroutineStats.min : '—' },
        { label: 'Go Max', value: goroutineStats ? goroutineStats.max : '—' },
        { label: 'Go Trend', value: goroutineStats ? (goroutineStats.trend >= 0 ? '+' : '') + goroutineStats.trend : '—', tone: (goroutineStats?.trend ?? 0) > 0 ? 'loss' : 'win' },
      ]}
    />
  {:else}
    <MetricsBar primary={[{ label: 'Status', value: 'Waiting for data...' }]} />
  {/if}

  <CollapsibleSection title="Memory" count={3} defaultOpen={true}>
    <div class="charts-grid">
      <LineChart title="Heap Allocation (MB)" series={cpuSeries} {store} yUnit=" MB" />
      <LineChart title="Memory Breakdown (MB)" series={rssSeries} {store} yUnit=" MB" />
      <LineChart title="Stack In-Use (MB)" series={stackSeries} {store} yUnit=" MB" />
    </div>
  </CollapsibleSection>

  <CollapsibleSection title="Runtime" count={3} defaultOpen={true}>
    <div class="charts-grid">
      <LineChart title="Goroutines" series={goroutineSeries} {store} />
      <LineChart title="Heap Objects" series={heapObjSeries} {store} />
      <LineChart title="CPU Usage (%)" series={cpuUsageSeries} {store} yUnit=" %" />
    </div>
  </CollapsibleSection>

  <CollapsibleSection title="GC & Allocations" count={3} defaultOpen={true}>
    <div class="charts-grid">
      <LineChart title="GC Count" series={gcSeries} {store} />
      <LineChart title="GC Pause Total (ms)" series={gcPauseSeries} {store} yUnit=" ms" />
      <LineChart title="Mallocs vs Frees" series={mallocFreesSeries} {store} />
    </div>
  </CollapsibleSection>
</div>

<style>
  .charts-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
    gap: 16px;
  }
</style>
