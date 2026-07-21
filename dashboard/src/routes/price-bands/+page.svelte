<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';
  import { api } from '$lib/api.js';
  import { setupChart } from '$lib/chart-init.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import ChartLoading from '$lib/components/ChartLoading.svelte';
  import { vibrantColor } from '$lib/utils.js';

  /** @type {any[]} */
  let rows = $state([]);
  let runTS = $state(0);
  let loading = $state(false);
  /** @type {string | null} */
  let error = $state(null);

  // Filters
  let selectedDay = $state('all');
  /** @type {Set<string>} */
  let selectedStrategies = $state(new Set());
  let minN = $state(5);
  let minWR = $state(55);
  let chartMetric = $state('win_rate');

  // Charts
  /** @type {any} */ let bandChart = null;
  let bandReady = $state(false);
  /** @type {HTMLCanvasElement | null} */ let bandCanvas = $state(null);
  /** @type {any} */ let nChart = null;
  let nReady = $state(false);
  /** @type {HTMLCanvasElement | null} */ let nCanvas = $state(null);

  /** @type {Record<string, string>} */
  const strategyColors = {
    'matchpoint': '#60a5fa',
    'matchpoint-aggro': '#a78bfa',
    'setpoint': '#34d399',
    'setpoint-serve': '#fbbf24',
    'setpoint-cheap': '#f472b0',
    'fadelongshot': '#f87171',
  };

  function colorFor(/** @type {string} */ name) {
    return strategyColors[name] || vibrantColor(name);
  }

  // Extract unique days + strategies from rows
  let allDays = $derived.by(() => {
    const s = new Set();
    for (const r of rows) s.add(r.day);
    return [...s].sort();
  });

  let allStrategies = $derived.by(() => {
    const s = new Set();
    for (const r of rows) s.add(r.strategy);
    return [...s].sort();
  });

  // Filtered rows based on day + strategy selection
  let filtered = $derived.by(() => {
    return rows.filter((r) => {
      if (selectedDay !== 'all' && r.day !== selectedDay) return false;
      if (selectedStrategies.size > 0 && !selectedStrategies.has(r.strategy)) return false;
      return true;
    });
  });

  // Best bands: N >= minN, WR >= minWR
  let bestBands = $derived.by(() => {
    return filtered
      .filter((r) => r.n >= minN && r.win_rate >= minWR)
      .sort((a, b) => b.win_rate - a.win_rate);
  });

  // Cross-strategy band totals (aggregate across strategies for same band)
  let bandTotals = $derived.by(() => {
    /** @type {Record<string, {label: string, lo: number, hi: number, n: number, wins: number, pnl: number, invested: number}>} */
    const m = {};
    for (const r of filtered) {
      const k = r.band_label;
      if (!m[k]) m[k] = { label: k, lo: r.band_lo, hi: r.band_hi, n: 0, wins: 0, pnl: 0, invested: 0 };
      m[k].n += r.n;
      m[k].wins += r.wins;
      m[k].pnl += r.net_pnl;
      m[k].invested += r.invested;
    }
    return Object.values(m).sort((a, b) => a.lo - b.lo);
  });

  // Tier-1: exclude fadelongshot*/nofade
  let tier1Rows = $derived.by(() => {
    return filtered.filter((r) => !r.strategy.startsWith('fadelongshot') && !r.strategy.includes('nofade'));
  });

  // Tier-1 cross-day summary
  let tier1Summary = $derived.by(() => {
    /** @type {Record<string, {strategy: string, n: number, wins: number, pnl: number, invested: number}>} */
    const m = {};
    for (const r of tier1Rows) {
      if (!m[r.strategy]) m[r.strategy] = { strategy: r.strategy, n: 0, wins: 0, pnl: 0, invested: 0 };
      m[r.strategy].n += r.n;
      m[r.strategy].wins += r.wins;
      m[r.strategy].pnl += r.net_pnl;
      m[r.strategy].invested += r.invested;
    }
    return Object.values(m).sort((a, b) => b.pnl - a.pnl);
  });

  // Tier-1 presence by day matrix
  let tier1Matrix = $derived.by(() => {
    /** @type {Record<string, Record<string, number>>} */
    const m = {};
    for (const r of tier1Rows) {
      if (!m[r.strategy]) m[r.strategy] = {};
      m[r.strategy][r.day] = (m[r.strategy][r.day] || 0) + r.n;
    }
    return m;
  });

  $effect(() => {
    chartMetric;
    selectedDay;
    selectedStrategies.size;
    rows.length;
    if (browser && rows.length > 0) renderCharts();
  });

  function toggleStrategy(/** @type {string} */ name) {
    const next = new Set(selectedStrategies);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selectedStrategies = next;
  }

  function toggleAllStrategies() {
    if (selectedStrategies.size === allStrategies.length) selectedStrategies = new Set();
    else selectedStrategies = new Set(allStrategies);
  }

  async function loadData() {
    loading = true;
    error = null;
    try {
      const data = await api.getPriceBandsSnapshot();
      rows = data.results || [];
      runTS = data.run_ts || 0;
      // Select all strategies by default
      if (selectedStrategies.size === 0 && allStrategies.length > 0) {
        selectedStrategies = new Set(allStrategies);
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function renderCharts() {
    if (!browser || rows.length === 0) return;
    const Chart = await setupChart();
    if (!Chart) return;

    bandReady = false;
    nReady = false;

    const selNames = selectedStrategies.size > 0 ? [...selectedStrategies] : allStrategies;

    // Band performance chart — grouped bars per band, one dataset per strategy
    if (bandChart) { bandChart.destroy(); bandChart = null; }
    if (bandCanvas) {
      const bandLabels = ['0.01-0.05','0.05-0.10','0.10-0.15','0.15-0.20','0.20-0.30','0.30-0.40','0.40-0.50','0.50-0.60','0.60-0.70','0.70-0.80','0.80-0.90','0.90-0.99'];

      /** @type {Record<string, Record<string, number>>} */
      const bandData = {};
      for (const r of filtered) {
        if (!bandData[r.strategy]) bandData[r.strategy] = {};
        const val = chartMetric === 'win_rate' ? r.win_rate : chartMetric === 'net_pnl' ? r.net_pnl : chartMetric === 'roi' ? r.roi : r.avg_edge;
        bandData[r.strategy][r.band_label] = (bandData[r.strategy][r.band_label] || 0) + val;
      }

      const datasets = selNames.map((name) => {
        const data = bandLabels.map((bl) => bandData[name]?.[bl ?? 0] || 0);
        return {
          label: name,
          data,
          backgroundColor: colorFor(name) + '80',
          borderColor: colorFor(name),
          borderWidth: 1,
        };
      }).filter((d) => d.data.some((v) => v !== 0));

      if (datasets.length > 0) {
        const metricLabel = chartMetric === 'win_rate' ? 'Win Rate (%)' : chartMetric === 'net_pnl' ? 'Net P&L ($)' : chartMetric === 'roi' ? 'ROI (%)' : 'Avg Edge (cents)';
        bandChart = new Chart(bandCanvas, {
          type: 'bar',
          data: { labels: bandLabels, datasets },
          options: {
            responsive: true, maintainAspectRatio: false, animation: false,
            plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
            scales: {
              x: { ticks: { color: '#64748b', font: { size: 9 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Price Band', color: '#64748b' } },
              y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: metricLabel, color: '#64748b' } },
            },
          },
        });
        bandReady = true;
      }
    }

    // N per band chart — stacked bars showing signal count per band per strategy
    if (nChart) { nChart.destroy(); nChart = null; }
    if (nCanvas) {
      const bandLabels = ['0.01-0.05','0.05-0.10','0.10-0.15','0.15-0.20','0.20-0.30','0.30-0.40','0.40-0.50','0.50-0.60','0.60-0.70','0.70-0.80','0.80-0.90','0.90-0.99'];

      /** @type {Record<string, Record<string, number>>} */
      const bandN = {};
      for (const r of filtered) {
        if (!bandN[r.strategy]) bandN[r.strategy] = {};
        bandN[r.strategy][r.band_label] = (bandN[r.strategy][r.band_label] || 0) + r.n;
      }

      const datasets = selNames.map((name) => {
        const data = bandLabels.map((bl) => bandN[name]?.[bl] || 0);
        return {
          label: name,
          data,
          backgroundColor: colorFor(name) + '80',
          borderColor: colorFor(name),
          borderWidth: 1,
        };
      }).filter((d) => d.data.some((v) => v !== 0));

      if (datasets.length > 0) {
        nChart = new Chart(nCanvas, {
          type: 'bar',
          data: { labels: bandLabels, datasets },
          options: {
            responsive: true, maintainAspectRatio: false, animation: false,
            plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
            scales: {
              x: { stacked: true, ticks: { color: '#64748b', font: { size: 9 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Price Band', color: '#64748b' } },
              y: { stacked: true, ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Signal Count', color: '#64748b' }, beginAtZero: true },
            },
          },
        });
        nReady = true;
      }
    }
  }

  let pollTimer = null;

  onMount(() => {
    if (browser) loadData();
    pollTimer = setInterval(() => { if (browser) loadData(); }, 300_000);
  });

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
    if (bandChart) bandChart.destroy();
    if (nChart) nChart.destroy();
  });
</script>

<svelte:head>
  <title>Price Bands — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="Price Bands" connected={!error} error={error || ''}>
    {#snippet children()}
      {#if loading}<Badge variant="loading" text="Loading..." />{/if}
      {#if runTS > 0}<Badge variant="ok" text={`Last run: ${new Date(runTS).toLocaleString()}`} />{/if}
    {/snippet}
  </PageHeader>

  {#if error && rows.length === 0}
    <div class="error-banner">{error}</div>
  {/if}

  {#if rows.length === 0 && !loading}
    <EmptyState text="No price band data yet. Cron computes missing days daily." />
  {:else if rows.length > 0}
    <div class="layout">
      <div class="main-content">
        <!-- Charts -->
        <div class="chart-section">
          <h2>Band Performance <span class="chart-subtitle">— by {chartMetric === 'win_rate' ? 'Win Rate' : chartMetric === 'net_pnl' ? 'Net P&L' : chartMetric === 'roi' ? 'ROI' : 'Avg Edge'}</span></h2>
          <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={bandCanvas}></canvas>{#if !bandReady}<ChartLoading />{/if}</div>
        </div>

        <div class="chart-section">
          <h2>Signal Count per Band</h2>
          <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={nCanvas}></canvas>{#if !nReady}<ChartLoading />{/if}</div>
        </div>

        <!-- Cross-strategy band totals -->
        <CollapsibleSection title="Cross-Strategy Band Totals" count={bandTotals.length} defaultOpen={true}>
          <table class="data-table">
            <thead><tr><th>Band</th><th class="num">N</th><th class="num">Wins</th><th class="num">Win Rate</th><th class="num">Net P&L</th><th class="num">ROI</th></tr></thead>
            <tbody>
              {#each bandTotals as bt}
                <tr>
                  <td class="mono">{bt.label}</td>
                  <td class="num">{bt.n}</td>
                  <td class="num">{bt.wins}</td>
                  <td class="num">{bt.n > 0 ? (bt.wins / bt.n * 100).toFixed(1) : '0.0'}%</td>
                  <td class="num" style="color: {bt.pnl >= 0 ? 'var(--win)' : 'var(--loss)'}">{bt.pnl >= 0 ? '+' : ''}{bt.pnl.toFixed(2)}</td>
                  <td class="num">{bt.invested > 0 ? (bt.pnl / bt.invested * 100).toFixed(1) : '0.0'}%</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </CollapsibleSection>

        <!-- Best bands -->
        <CollapsibleSection title="Best Bands (N≥{minN}, WR≥{minWR}%)" count={bestBands.length} defaultOpen={true}>
          {#if bestBands.length === 0}
            <div class="empty-mini">No bands meet thresholds. Adjust filters.</div>
          {:else}
            <table class="data-table">
              <thead><tr><th>Day</th><th>Strategy</th><th>Band</th><th class="num">N</th><th class="num">Wins</th><th class="num">Win Rate</th><th class="num">Net P&L</th><th class="num">ROI</th><th class="num">Avg Edge</th></tr></thead>
              <tbody>
                {#each bestBands as r}
                  <tr>
                    <td class="mono">{r.day}</td>
                    <td><span class="dot" style="background: {colorFor(r.strategy)}"></span> {r.strategy}</td>
                    <td class="mono">{r.band_label}</td>
                    <td class="num">{r.n}</td>
                    <td class="num">{r.wins}</td>
                    <td class="num">{r.win_rate.toFixed(1)}%</td>
                    <td class="num" style="color: {r.net_pnl >= 0 ? 'var(--win)' : 'var(--loss)'}">{r.net_pnl >= 0 ? '+' : ''}{r.net_pnl.toFixed(2)}</td>
                    <td class="num">{r.roi.toFixed(1)}%</td>
                    <td class="num">{r.avg_edge.toFixed(1)}c</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </CollapsibleSection>

        <!-- Per-strategy-per-band detail -->
        <CollapsibleSection title="Per-Strategy Per-Band Detail" count={filtered.length} defaultOpen={false}>
          <table class="data-table">
            <thead><tr><th>Day</th><th>Strategy</th><th>Band</th><th class="num">N</th><th class="num">Wins</th><th class="num">Win Rate</th><th class="num">Net P&L</th><th class="num">ROI</th><th class="num">Avg Edge</th></tr></thead>
            <tbody>
              {#each filtered.sort((a, b) => a.day.localeCompare(b.day) || a.strategy.localeCompare(b.strategy) || a.band_lo - b.band_lo) as r}
                <tr>
                  <td class="mono">{r.day}</td>
                  <td><span class="dot" style="background: {colorFor(r.strategy)}"></span> {r.strategy}</td>
                  <td class="mono">{r.band_label}</td>
                  <td class="num">{r.n}</td>
                  <td class="num">{r.wins}</td>
                  <td class="num">{r.win_rate.toFixed(1)}%</td>
                  <td class="num" style="color: {r.net_pnl >= 0 ? 'var(--win)' : 'var(--loss)'}">{r.net_pnl >= 0 ? '+' : ''}{r.net_pnl.toFixed(2)}</td>
                  <td class="num">{r.roi.toFixed(1)}%</td>
                  <td class="num">{r.avg_edge.toFixed(1)}c</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </CollapsibleSection>

        <!-- Tier-1 cross-day summary -->
        <CollapsibleSection title="Tier-1 Cross-Day Summary (excl. fadelongshot*/nofade)" count={tier1Summary.length} defaultOpen={false}>
          <table class="data-table">
            <thead><tr><th>Strategy</th><th class="num">N</th><th class="num">Wins</th><th class="num">Win Rate</th><th class="num">Net P&L</th><th class="num">ROI</th></tr></thead>
            <tbody>
              {#each tier1Summary as s}
                <tr>
                  <td><span class="dot" style="background: {colorFor(s.strategy)}"></span> {s.strategy}</td>
                  <td class="num">{s.n}</td>
                  <td class="num">{s.wins}</td>
                  <td class="num">{s.n > 0 ? (s.wins / s.n * 100).toFixed(1) : '0.0'}%</td>
                  <td class="num" style="color: {s.pnl >= 0 ? 'var(--win)' : 'var(--loss)'}">{s.pnl >= 0 ? '+' : ''}{s.pnl.toFixed(2)}</td>
                  <td class="num">{s.invested > 0 ? (s.pnl / s.invested * 100).toFixed(1) : '0.0'}%</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </CollapsibleSection>

        <!-- Tier-1 presence by day matrix -->
        <CollapsibleSection title="Tier-1 Presence by Day" count={Object.keys(tier1Matrix).length} defaultOpen={false}>
          <table class="data-table">
            <thead>
              <tr><th>Strategy</th>{#each allDays as d}<th class="num">{d}</th>{/each}</tr>
            </thead>
            <tbody>
              {#each Object.entries(tier1Matrix).sort((a, b) => a[0].localeCompare(b[0])) as [strat, dayMap]}
                <tr>
                  <td><span class="dot" style="background: {colorFor(strat)}"></span> {strat}</td>
                  {#each allDays as d}
                    <td class="num">{dayMap[d] || ''}</td>
                  {/each}
                </tr>
              {/each}
            </tbody>
          </table>
        </CollapsibleSection>
      </div>

      <!-- Filter sidebar -->
      <aside class="filter-sidebar">
        <div class="filter-group">
          <h3>Day</h3>
          <select bind:value={selectedDay} class="day-select">
            <option value="all">All Days</option>
            {#each allDays as d}
              <option value={d}>{d}</option>
            {/each}
          </select>
        </div>

        <div class="filter-group">
          <h3>Strategies</h3>
          <button class="toggle-all" onclick={toggleAllStrategies}>
            {selectedStrategies.size === allStrategies.length ? 'Deselect All' : 'Select All'}
          </button>
          <div class="strategy-list">
            {#each allStrategies as name}
              <button
                class="toggle-btn"
                class:active={selectedStrategies.has(name)}
                style="--btn-color: {colorFor(name)}"
                onclick={() => toggleStrategy(name)}
              >
                <span class="dot" style="background: {colorFor(name)}"></span>
                {name}
              </button>
            {/each}
          </div>
        </div>

        <div class="filter-group">
          <h3>Chart Metric</h3>
          <label class="filter-label">
            <select bind:value={chartMetric}>
              <option value="win_rate">Win Rate</option>
              <option value="net_pnl">Net P&L</option>
              <option value="roi">ROI</option>
              <option value="avg_edge">Avg Edge</option>
            </select>
          </label>
        </div>

        <div class="filter-group">
          <h3>Best Bands Filter</h3>
          <label class="filter-label">Min N
            <input type="number" bind:value={minN} min="1" max="100" step="1" />
          </label>
          <label class="filter-label">Min Win Rate %
            <input type="number" bind:value={minWR} min="0" max="100" step="1" />
          </label>
        </div>

        <div class="filter-group">
          <h3>Data</h3>
          <button class="run-btn" onclick={loadData} disabled={loading}>
            {loading ? 'Loading...' : 'Refresh'}
          </button>
        </div>
      </aside>
    </div>
  {/if}
</div>

<style>
  .error-banner { background: var(--loss-bg); color: var(--loss); padding: 12px 16px; border-radius: var(--radius); margin-bottom: 16px; font-size: 13px; }
  .layout { display: flex; gap: 20px; align-items: flex-start; }
  .main-content { flex: 1; min-width: 0; }
  .filter-sidebar { width: 240px; flex-shrink: 0; position: sticky; top: 16px; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; max-height: calc(100vh - 32px); overflow-y: auto; }
  .filter-group { margin-bottom: 20px; }
  .filter-group:last-child { margin-bottom: 0; }
  .filter-group h3 { font-size: 12px; font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; margin: 0 0 10px 0; }
  .strategy-list { display: flex; flex-direction: column; gap: 6px; margin-bottom: 8px; }
  .toggle-all { background: var(--surface-hover); border: 1px solid var(--border-strong); color: #94a3b8; padding: 6px 12px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; margin-bottom: 8px; width: 100%; text-align: left; }
  .toggle-all:hover { background: var(--border-strong); }
  .toggle-btn { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text-muted); padding: 6px 10px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; display: flex; align-items: center; gap: 6px; transition: all 0.15s; text-align: left; }
  .toggle-btn.active { border-color: var(--btn-color); color: var(--text); }
  .toggle-btn:hover { border-color: var(--btn-color); }
  .filter-label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--text-muted); margin-bottom: 10px; }
  .filter-label input, .filter-label select, .day-select { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 5px 10px; border-radius: var(--radius-xs); font-size: 13px; width: 100%; box-sizing: border-box; }
  .run-btn { background: #1e40af; border: 1px solid #3b82f6; color: var(--text); padding: 6px 16px; border-radius: var(--radius-sm); font-size: 13px; font-weight: 600; cursor: pointer; width: 100%; }
  .run-btn:hover { background: #2563eb; }
  .run-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; display: inline-block; }
  .chart-section { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; margin-bottom: 16px; }
  .chart-section h2 { font-size: 14px; font-weight: 600; color: #cbd5e1; margin: 0 0 12px 0; }
  .chart-subtitle { font-size: 12px; color: var(--text-muted); font-weight: 400; }
  .empty-mini { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 16px; text-align: center; color: var(--text-dim); font-size: 13px; }
</style>
