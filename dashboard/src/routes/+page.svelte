<script>
  import Chart from '$lib/Chart.svelte';
  import { createMetricsStore } from '$lib/metrics.js';

  const METRICS_URL = 'http://127.0.0.1:6060/metrics';
  const POLL_MS = 1000;

  const store = createMetricsStore(METRICS_URL, POLL_MS);

  // Series definitions
  const cpuSeries = [
    {
      label: 'Heap (MB)',
      getValue: (s) => Math.round((s.heap_alloc_bytes / 1048576) * 10) / 10,
      color: '#60a5fa',
    },
  ];

  const rssSeries = [
    {
      label: 'Heap (MB)',
      getValue: (s) => Math.round((s.heap_alloc_bytes / 1048576) * 10) / 10,
      color: '#60a5fa',
    },
    {
      label: 'Heap Sys (MB)',
      getValue: (s) => Math.round((s.heap_sys_bytes / 1048576) * 10) / 10,
      color: '#a78bfa',
    },
    {
      label: 'Stack (MB)',
      getValue: (s) => Math.round((s.stack_inuse_bytes / 1048576) * 10) / 10,
      color: '#f472b0',
    },
    {
      label: 'Total Sys (MB)',
      getValue: (s) => Math.round((s.sys_bytes / 1048576) * 10) / 10,
      color: '#fbbf24',
    },
  ];

  const goroutineSeries = [
    {
      label: 'Goroutines',
      getValue: (s) => s.goroutines,
      color: '#34d399',
    },
  ];

  const gcSeries = [
    {
      label: 'GC Count',
      getValue: (s) => s.gc_num,
      color: '#fbbf24',
    },
  ];

  const gcPauseSeries = [
    {
      label: 'GC Pause (ms)',
      getValue: (s) => Math.round((s.gc_pause_ns / 1e6) * 100) / 100,
      color: '#f87171',
    },
  ];

  const heapObjSeries = [
    {
      label: 'Heap Objects',
      getValue: (s) => s.heap_objects,
      color: '#60a5fa',
    },
  ];

  const mallocFreesSeries = [
    {
      label: 'Mallocs',
      getValue: (s) => s.mallocs,
      color: '#34d399',
    },
    {
      label: 'Frees',
      getValue: (s) => s.frees,
      color: '#f87171',
    },
  ];

  function fmtBytes(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / 1048576).toFixed(1) + ' MB';
  }

  function fmtNum(n) {
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
    return String(n);
  }
</script>

<svelte:head>
  <title>Ghost Trader Dashboard</title>
</svelte:head>

<div class="dashboard">
  <header>
    <h1>Ghost Trader Dashboard</h1>
    <div class="status">
      {#if $store.connected}
        <span class="badge ok">Connected</span>
      {:else}
        <span class="badge err">Disconnected</span>
      {/if}
      {#if $store.error}
        <span class="error">{$store.error}</span>
      {/if}
    </div>
  </header>

  {#if $store.current}
    <div class="stats-grid">
      <div class="stat-card">
        <div class="stat-label">Goroutines</div>
        <div class="stat-value">{$store.current.goroutines}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Heap</div>
        <div class="stat-value">{fmtBytes($store.current.heap_alloc_bytes)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Heap Objects</div>
        <div class="stat-value">{fmtNum($store.current.heap_objects)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Total Sys</div>
        <div class="stat-value">{fmtBytes($store.current.sys_bytes)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">GC Count</div>
        <div class="stat-value">{$store.current.gc_num}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">GC Pause</div>
        <div class="stat-value">
          {($store.current.gc_pause_ns / 1e6).toFixed(2)} ms
        </div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Mallocs</div>
        <div class="stat-value">{fmtNum($store.current.mallocs)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Frees</div>
        <div class="stat-value">{fmtNum($store.current.frees)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">CPUs</div>
        <div class="stat-value">{$store.current.num_cpu}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Next GC</div>
        <div class="stat-value">{fmtBytes($store.current.next_gc_bytes)}</div>
      </div>
    </div>
  {:else}
    <div class="stats-grid">
      <div class="stat-card placeholder">
        <div class="stat-label">Waiting for data...</div>
      </div>
    </div>
  {/if}

  <div class="charts-grid">
    <Chart title="Heap Allocation (MB)" series={cpuSeries} {store} yUnit=" MB" />
    <Chart title="Memory Breakdown (MB)" series={rssSeries} {store} yUnit=" MB" />
    <Chart title="Goroutines" series={goroutineSeries} {store} />
    <Chart title="Heap Objects" series={heapObjSeries} {store} />
    <Chart title="GC Count" series={gcSeries} {store} />
    <Chart title="GC Pause Total (ms)" series={gcPauseSeries} {store} yUnit=" ms" />
    <Chart title="Mallocs vs Frees" series={mallocFreesSeries} {store} />
    <Chart title="Stack In-Use (MB)" series={[{ label: 'Stack (MB)', getValue: (s) => Math.round((s.stack_inuse_bytes / 1048576) * 10) / 10, color: '#f472b0' }]} {store} yUnit=" MB" />
  </div>
</div>

<style>
  :global(body) {
    background: #020617;
    color: #e2e8f0;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    margin: 0;
  }

  .dashboard {
    max-width: 1400px;
    margin: 0 auto;
    padding: 20px;
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 20px;
  }

  h1 {
    font-size: 22px;
    font-weight: 700;
    margin: 0;
    color: #f1f5f9;
  }

  .status {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .badge {
    padding: 4px 10px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
  }

  .badge.ok {
    background: #064e3b;
    color: #34d399;
  }

  .badge.err {
    background: #450a0a;
    color: #f87171;
  }

  .error {
    font-size: 12px;
    color: #f87171;
  }

  .stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
    gap: 10px;
    margin-bottom: 20px;
  }

  .stat-card {
    background: #0f172a;
    border: 1px solid #1e293b;
    border-radius: 8px;
    padding: 12px;
  }

  .stat-label {
    font-size: 11px;
    color: #64748b;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .stat-value {
    font-size: 20px;
    font-weight: 700;
    color: #f1f5f9;
    margin-top: 4px;
  }

  .placeholder {
    grid-column: 1 / -1;
    text-align: center;
    padding: 30px;
  }

  .charts-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
    gap: 14px;
  }
</style>
