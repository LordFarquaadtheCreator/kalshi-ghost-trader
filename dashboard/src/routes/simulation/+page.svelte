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
  let strategies = $state([]);
  /** @type {Set<string>} */
  let selected = $state(new Set());
  /** @type {any[]} */
  let summaries = $state([]);
  /** @type {any[]} */
  let bands = $state([]);
  let runTS = $state(0);
  let loading = $state(false);
  /** @type {string | null} */
  let error = $state(null);
  let selectedDay = $state('all');
  let chartMetric = $state('win_rate');
  let minN = $state(5);
  let minWR = $state(55);

  // Charts
  /** @type {any} */ let pnlChart = null;
  let pnlReady = $state(false);
  /** @type {HTMLCanvasElement | null} */ let pnlCanvas = $state(null);
  /** @type {any} */ let winlossChart = null;
  let winlossReady = $state(false);
  /** @type {HTMLCanvasElement | null} */ let winlossCanvas = $state(null);
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

  let allDays = $derived.by(() => {
    const s = new Set();
    for (const b of bands) s.add(b.day);
    return [...s].sort();
  });

  // Filtered bands by day + strategy selection
  let filteredBands = $derived.by(() => {
    return bands.filter((b) => {
      if (selectedDay !== 'all' && b.day !== selectedDay) return false;
      if (selected.size > 0 && !selected.has(b.strategy)) return false;
      return true;
    });
  });

  // Summary lookup by strategy name
  let summaryMap = $derived.by(() => {
    /** @type {Record<string, any>} */
    const m = {};
    for (const s of summaries) m[s.name] = s;
    return m;
  });

  // Best bands: N >= minN, WR >= minWR
  let bestBands = $derived.by(() => {
    return filteredBands
      .filter((b) => b.n >= minN && b.win_rate >= minWR)
      .sort((a, b) => b.win_rate - a.win_rate);
  });

  // Peak bands per strategy
  let peakBands = $derived.by(() => {
    /** @type {Record<string, any[]>} */
    const m = {};
    for (const b of filteredBands) {
      if (b.peak) {
        if (!m[b.strategy]) m[b.strategy] = [];
        m[b.strategy].push(b);
      }
    }
    for (const k in m) m[k].sort((a, b) => b.score - a.score);
    return m;
  });

  // Cross-strategy band totals (aggregate across strategies for same band)
  let bandTotals = $derived.by(() => {
    /** @type {Record<string, {label: string, lo: number, hi: number, n: number, wins: number, pnl: number, invested: number}>} */
    const m = {};
    for (const b of filteredBands) {
      const k = b.band_label;
      if (!m[k]) m[k] = { label: k, lo: b.band_lo, hi: b.band_hi, n: 0, wins: 0, pnl: 0, invested: 0 };
      m[k].n += b.n;
      m[k].wins += b.wins;
      m[k].pnl += b.net_pnl;
      m[k].invested += b.invested;
    }
    return Object.values(m).sort((a, b) => a.lo - b.lo);
  });

  $effect(() => {
    chartMetric;
    selectedDay;
    selected.size;
    bands.length;
    summaries.length;
    if (browser && summaries.length > 0) renderCharts();
  });

  function toggle(/** @type {string} */ name) {
    const next = new Set(selected);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selected = next;
  }

  function toggleAll() {
    if (selected.size === strategies.length) selected = new Set();
    else selected = new Set(strategies);
  }

  async function loadStrategies() {
    try {
      const data = await api.getStrategies();
      strategies = data.strategies || [];
      selected = new Set(strategies);
    } catch (err) {
      error = 'Cannot reach strategy API on :6060.';
    }
  }

  async function loadSimulation() {
    if (selected.size === 0) return;
    loading = true;
    error = null;
    try {
      const data = await api.getSimulation();
      summaries = data.summaries || [];
      bands = data.bands || [];
      runTS = data.insight_run_ts || 0;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function renderCharts() {
    if (!browser || summaries.length === 0) return;
    const Chart = await setupChart();
    if (!Chart) return;

    pnlReady = false;
    winlossReady = false;
    bandReady = false;
    nReady = false;

    const selNames = [...selected];

    // Cumulative P&L — from pre-computed cum_pnl series per strategy
    if (pnlChart) { pnlChart.destroy(); pnlChart = null; }
    if (pnlCanvas) {
      const datasets = selNames.map((name) => {
        const s = summaryMap[name];
        if (!s || !s.cum_pnl || s.cum_pnl.length === 0) return null;
        return {
          label: name,
          data: s.cum_pnl.map((/** @type {any} */ p) => p.pnl),
          borderColor: colorFor(name),
          backgroundColor: colorFor(name) + '20',
          borderWidth: 2, pointRadius: 0, tension: 0.2,
        };
      }).filter(Boolean);

      if (datasets.length > 0) {
        const maxLen = Math.max(.../** @type {any[]} */ (datasets).map((d) => d.data.length), 0);
        pnlChart = new Chart(pnlCanvas, {
          type: 'line',
          data: { labels: Array.from({ length: maxLen }, (_, i) => i + 1), datasets },
          options: {
            responsive: true, maintainAspectRatio: false, animation: false,
            plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } }, tooltip: { mode: 'index', intersect: false } },
            scales: {
              x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Order #', color: '#64748b' } },
              y: { ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { color: '#1e293b' }, title: { display: true, text: 'Cumulative P&L ($)', color: '#64748b' } },
            },
          },
        });
        pnlReady = true;
      }
    }

    // Win / Loss bars — from summary
    if (winlossChart) { winlossChart.destroy(); winlossChart = null; }
    if (winlossCanvas) {
      winlossChart = new Chart(winlossCanvas, {
        type: 'bar',
        data: {
          labels: selNames,
          datasets: [
            { label: 'Wins', data: selNames.map((n) => summaryMap[n]?.summary?.wins || 0), backgroundColor: '#34d399' },
            { label: 'Losses', data: selNames.map((n) => (summaryMap[n]?.summary?.total_signals || 0) - (summaryMap[n]?.summary?.wins || 0)), backgroundColor: '#f87171' },
          ],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 11 } }, grid: { color: '#1e293b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, beginAtZero: true },
          },
        },
      });
      winlossReady = true;
    }

    const bandLabels = ['0.01-0.05','0.05-0.10','0.10-0.15','0.15-0.20','0.20-0.30','0.30-0.40','0.40-0.50','0.50-0.60','0.60-0.70','0.70-0.80','0.80-0.90','0.90-0.99'];

    // Band performance chart — grouped bars per band, one dataset per strategy
    if (bandChart) { bandChart.destroy(); bandChart = null; }
    if (bandCanvas) {
      /** @type {Record<string, Record<string, number>>} */
      const bandData = {};
      for (const b of filteredBands) {
        if (!bandData[b.strategy]) bandData[b.strategy] = {};
        const val = chartMetric === 'win_rate' ? b.win_rate : chartMetric === 'net_pnl' ? b.net_pnl : chartMetric === 'roi' ? b.roi : chartMetric === 'sharpe' ? b.sharpe : b.avg_edge;
        bandData[b.strategy][b.band_label] = (bandData[b.strategy][b.band_label] || 0) + val;
      }

      const datasets = selNames.map((name) => {
        const data = bandLabels.map((bl) => bandData[name]?.[bl] || 0);
        return {
          label: name,
          data,
          backgroundColor: colorFor(name) + '80',
          borderColor: colorFor(name),
          borderWidth: 1,
        };
      }).filter((d) => d.data.some((v) => v !== 0));

      if (datasets.length > 0) {
        const metricLabel = chartMetric === 'win_rate' ? 'Win Rate (%)' : chartMetric === 'net_pnl' ? 'Net P&L ($)' : chartMetric === 'roi' ? 'ROI (%)' : chartMetric === 'sharpe' ? 'Sharpe' : 'Avg Edge (cents)';
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

    // Signal count per band — stacked bars
    if (nChart) { nChart.destroy(); nChart = null; }
    if (nCanvas) {
      /** @type {Record<string, Record<string, number>>} */
      const bandN = {};
      for (const b of filteredBands) {
        if (!bandN[b.strategy]) bandN[b.strategy] = {};
        bandN[b.strategy][b.band_label] = (bandN[b.strategy][b.band_label] || 0) + b.n;
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

  onMount(() => {
    if (browser) loadStrategies().then(() => loadSimulation());
  });

  onDestroy(() => {
    if (pnlChart) pnlChart.destroy();
    if (winlossChart) winlossChart.destroy();
    if (bandChart) bandChart.destroy();
    if (nChart) nChart.destroy();
  });
</script>

<svelte:head>
  <title>Simulation — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="Simulation" connected={!error} error={error || ''}>
    {#snippet children()}
      {#if loading}<Badge variant="loading" text="Loading..." />{/if}
      {#if error}<Badge variant="err" text="API Error" />{/if}
      {#if runTS > 0}<Badge variant="ok" text={`Insights: ${new Date(runTS).toLocaleString()}`} />{/if}
    {/snippet}
  </PageHeader>

  {#if error && summaries.length === 0}
    <div class="error-banner">{error}</div>
  {/if}

  {#if summaries.length === 0 && !loading}
    <EmptyState text="No simulation data yet. Cron computes insights daily." />
  {:else if summaries.length > 0}
    <div class="layout">
      <div class="main-content">
        <!-- Summary cards -->
        <div class="summary-grid">
          {#each [...selected] as name}
            {@const s = summaryMap[name]}
            {#if s && s.summary}
              <div class="summary-card" style="--accent: {colorFor(name)}">
                <div class="summary-header">
                  <span class="dot" style="background: {colorFor(name)}"></span>
                  {name}
                </div>
                <div class="summary-stats">
                  <div class="stat"><span class="stat-label">Signals</span><span class="stat-val">{s.summary.total_signals}</span></div>
                  <div class="stat"><span class="stat-label">Win Rate</span><span class="stat-val">{s.summary.win_rate.toFixed(1)}%</span></div>
                  <div class="stat"><span class="stat-label">Net P&L</span><span class="stat-val" class:positive={s.summary.net_pnl > 0} class:negative={s.summary.net_pnl < 0}>${s.summary.net_pnl.toFixed(2)}</span></div>
                  <div class="stat"><span class="stat-label">ROI</span><span class="stat-val">{s.summary.roi.toFixed(1)}%</span></div>
                  <div class="stat"><span class="stat-label">Sharpe</span><span class="stat-val">{s.summary.sharpe.toFixed(2)}</span></div>
                  <div class="stat"><span class="stat-label">Profit Factor</span><span class="stat-val">{s.summary.profit_factor.toFixed(2)}</span></div>
                  <div class="stat"><span class="stat-label">Avg Edge</span><span class="stat-val">{s.summary.avg_edge.toFixed(1)}c</span></div>
                  <div class="stat"><span class="stat-label">Max DD</span><span class="stat-val">${s.summary.max_drawdown.toFixed(2)}</span></div>
                </div>
              </div>
            {/if}
          {/each}
        </div>

        <!-- Charts -->
        <div class="chart-section">
          <h2>Cumulative P&L</h2>
          <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={pnlCanvas}></canvas>{#if !pnlReady}<ChartLoading />{/if}</div>
        </div>

        <div class="chart-section">
          <h2>Win / Loss Comparison</h2>
          <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={winlossCanvas}></canvas>{#if !winlossReady}<ChartLoading />{/if}</div>
        </div>

        <div class="chart-section">
          <h2>Band Performance <span class="chart-subtitle">— by {chartMetric === 'win_rate' ? 'Win Rate' : chartMetric === 'net_pnl' ? 'Net P&L' : chartMetric === 'roi' ? 'ROI' : chartMetric === 'sharpe' ? 'Sharpe' : 'Avg Edge'}</span></h2>
          <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={bandCanvas}></canvas>{#if !bandReady}<ChartLoading />{/if}</div>
        </div>

        <div class="chart-section">
          <h2>Signal Count per Band</h2>
          <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={nCanvas}></canvas>{#if !nReady}<ChartLoading />{/if}</div>
        </div>

        <!-- Peak cards -->
        {#if Object.keys(peakBands).length > 0}
          <div class="chart-section">
            <h2>Peak Bands <span class="chart-subtitle">— local maxima above median score</span></h2>
            <div class="peak-cards">
              {#each [...selected] as name}
                {@const peaks = peakBands[name]}
                {#if peaks && peaks.length > 0}
                  <div class="peak-card">
                    <div class="peak-card-header">
                      <span class="dot" style="background: {colorFor(name)}"></span>
                      {name}
                      <span class="peak-count">{peaks.length} peak{peaks.length > 1 ? 's' : ''}</span>
                    </div>
                    {#each peaks as p}
                      <div class="peak-row">
                        <span class="peak-range">{p.band_label}</span>
                        <span class="peak-stat">{p.win_rate.toFixed(1)}% WR</span>
                        <span class="peak-stat">{p.n} sig</span>
                        <span class="peak-stat" class:positive={p.net_pnl > 0} class:negative={p.net_pnl < 0}>${p.net_pnl.toFixed(2)}</span>
                        <span class="peak-stat">score {p.score.toFixed(3)}</span>
                      </div>
                    {/each}
                  </div>
                {/if}
              {/each}
            </div>
          </div>
        {/if}

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
              <thead><tr><th>Day</th><th>Strategy</th><th>Band</th><th class="num">N</th><th class="num">Wins</th><th class="num">Win Rate</th><th class="num">Net P&L</th><th class="num">ROI</th><th class="num">Sharpe</th><th class="num">PF</th><th class="num">Max DD</th></tr></thead>
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
                    <td class="num">{r.sharpe.toFixed(2)}</td>
                    <td class="num">{r.profit_factor.toFixed(2)}</td>
                    <td class="num">${r.max_drawdown.toFixed(2)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </CollapsibleSection>

        <!-- Per-strategy per-band detail -->
        <CollapsibleSection title="Per-Strategy Per-Band Detail" count={filteredBands.length} defaultOpen={false}>
          <table class="data-table">
            <thead><tr><th>Day</th><th>Strategy</th><th>Band</th><th class="num">N</th><th class="num">Wins</th><th class="num">Win Rate</th><th class="num">Net P&L</th><th class="num">ROI</th><th class="num">Sharpe</th><th class="num">PF</th><th class="num">Max DD</th><th class="num">Score</th><th>Peak</th></tr></thead>
            <tbody>
              {#each filteredBands.sort((a, b) => a.day.localeCompare(b.day) || a.strategy.localeCompare(b.strategy) || a.band_lo - b.band_lo) as r}
                <tr>
                  <td class="mono">{r.day}</td>
                  <td><span class="dot" style="background: {colorFor(r.strategy)}"></span> {r.strategy}</td>
                  <td class="mono">{r.band_label}</td>
                  <td class="num">{r.n}</td>
                  <td class="num">{r.wins}</td>
                  <td class="num">{r.win_rate.toFixed(1)}%</td>
                  <td class="num" style="color: {r.net_pnl >= 0 ? 'var(--win)' : 'var(--loss)'}">{r.net_pnl >= 0 ? '+' : ''}{r.net_pnl.toFixed(2)}</td>
                  <td class="num">{r.roi.toFixed(1)}%</td>
                  <td class="num">{r.sharpe.toFixed(2)}</td>
                  <td class="num">{r.profit_factor.toFixed(2)}</td>
                  <td class="num">${r.max_drawdown.toFixed(2)}</td>
                  <td class="num">{r.score.toFixed(3)}</td>
                  <td>{r.peak ? '★' : ''}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </CollapsibleSection>
      </div>

      <!-- Filter sidebar -->
      <aside class="filter-sidebar">
        <div class="filter-group">
          <h3>Strategies</h3>
          <button class="toggle-all" onclick={toggleAll}>
            {selected.size === strategies.length ? 'Deselect All' : 'Select All'}
          </button>
          <div class="strategy-list">
            {#each strategies as name}
              <button
                class="toggle-btn"
                class:active={selected.has(name)}
                style="--btn-color: {colorFor(name)}"
                onclick={() => toggle(name)}
              >
                <span class="dot" style="background: {colorFor(name)}"></span>
                {name}
              </button>
            {/each}
          </div>
        </div>

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
          <h3>Chart Metric</h3>
          <label class="filter-label">
            <select bind:value={chartMetric}>
              <option value="win_rate">Win Rate</option>
              <option value="net_pnl">Net P&L</option>
              <option value="roi">ROI</option>
              <option value="sharpe">Sharpe</option>
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
          <button class="run-btn" onclick={loadSimulation} disabled={loading}>
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
  .summary-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 12px; margin-bottom: 20px; }
  .summary-card { background: var(--surface); border: 1px solid var(--border); border-left: 3px solid var(--accent); border-radius: var(--radius); padding: 14px; }
  .summary-header { display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; color: var(--text-bright); margin-bottom: 10px; }
  .summary-stats { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
  .stat { display: flex; flex-direction: column; }
  .stat-label { font-size: 10px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; }
  .stat-val { font-size: 15px; font-weight: 700; color: var(--text-bright); }
  .stat-val.positive { color: var(--win); }
  .stat-val.negative { color: var(--loss); }
  .peak-cards { display: flex; flex-direction: column; gap: 10px; margin-top: 14px; }
  .peak-card { background: var(--surface-hover); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 10px 14px; }
  .peak-card-header { display: flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 600; color: var(--text-bright); margin-bottom: 8px; }
  .peak-count { font-size: 11px; color: var(--text-muted); font-weight: 400; margin-left: auto; }
  .peak-row { display: flex; align-items: center; gap: 14px; font-size: 12px; color: var(--text-muted); padding: 3px 0; }
  .peak-range { font-weight: 600; color: var(--text); min-width: 100px; }
  .peak-stat { min-width: 70px; }
  .peak-stat.positive { color: var(--win); }
  .peak-stat.negative { color: var(--loss); }
  .empty-mini { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 16px; text-align: center; color: var(--text-dim); font-size: 13px; }
</style>
