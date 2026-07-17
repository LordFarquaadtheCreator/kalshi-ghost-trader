<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';
  import { api } from '$lib/api.js';
  import { setupChart } from '$lib/chart-init.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';

  /** @type {string[]} */
  let strategies = $state([]);
  let selected = $state(new Set());
  /** @type {Record<string, any>} */
  let results = $state({});
  let loading = $state(false);
  /** @type {string | null} */
  let error = $state(null);
  let minPrice = $state(0);
  let lastRun = $state(0);
  let filterResult = $state('');
  let filterMatch = $state('');

  /** @type {any} */ let pnlChart = null;
  /** @type {any} */ let winlossChart = null;
  /** @type {any} */ let priceDistChart = null;
  /** @type {HTMLCanvasElement | null} */ let pnlCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let winlossCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let priceDistCanvas = $state(null);

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
    return strategyColors[name] || '#94a3b8';
  }

  async function loadStrategies() {
    try {
      const data = await api.getStrategies();
      strategies = data.strategies || [];
      selected = new Set(strategies);
    } catch (err) {
      error = 'Cannot reach strategy API on :6061. Start: go run ./cmd/strategy-api';
    }
  }

  async function runBacktest() {
    if (selected.size === 0) return;
    loading = true;
    error = null;
    try {
      const data = await api.runBacktest([...selected], minPrice);
      results = {};
      for (const r of data.results || []) {
        results[r.name] = r;
      }
      lastRun = Date.now();
      await renderCharts();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

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

  function filterOrders(/** @type {any[]} */ orders) {
    return orders.filter((o) => {
      if (filterResult === 'won' && !o.won) return false;
      if (filterResult === 'lost' && o.won) return false;
      if (filterMatch && !o.match.toLowerCase().includes(filterMatch.toLowerCase())) return false;
      return true;
    });
  }

  function cumulativePnL(/** @type {any[]} */ orders) {
    let cum = 0;
    return orders.map((o) => {
      cum += o.pnl;
      return Math.round(cum * 100) / 100;
    });
  }

  async function renderCharts() {
    if (!browser || Object.keys(results).length === 0) return;
    const Chart = await setupChart();
    if (!Chart) return;

    const selNames = [...selected];
    const hasData = selNames.some((n) => results[n] && results[n].orders.length > 0);

    if (pnlChart) { pnlChart.destroy(); pnlChart = null; }
    if (pnlCanvas && hasData) {
      const datasets = selNames.map((name) => {
        const r = results[name];
        if (!r || r.orders.length === 0) return null;
        return {
          label: name,
          data: cumulativePnL(r.orders),
          borderColor: colorFor(name),
          backgroundColor: colorFor(name) + '20',
          borderWidth: 2, pointRadius: 0, tension: 0.2,
        };
      }).filter(Boolean);

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
    }

    if (winlossChart) { winlossChart.destroy(); winlossChart = null; }
    if (winlossCanvas && hasData) {
      winlossChart = new Chart(winlossCanvas, {
        type: 'bar',
        data: {
          labels: selNames,
          datasets: [
            { label: 'Wins', data: selNames.map((n) => results[n]?.summary.wins || 0), backgroundColor: '#34d399' },
            { label: 'Losses', data: selNames.map((n) => results[n]?.summary.losses || 0), backgroundColor: '#f87171' },
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
    }

    if (priceDistChart) { priceDistChart.destroy(); priceDistChart = null; }
    if (priceDistCanvas && hasData) {
      const labels = Array.from({ length: 10 }, (_, i) => `${i * 10}-${(i + 1) * 10}c`);
      const datasets = selNames.map((name) => {
        const r = results[name];
        if (!r || r.orders.length === 0) return null;
        const bins = new Array(10).fill(0);
        for (const o of r.orders) {
          const idx = Math.min(Math.floor(o.price * 10), 9);
          bins[idx]++;
        }
        return { label: name, data: bins, backgroundColor: colorFor(name) + '80', borderColor: colorFor(name), borderWidth: 1 };
      }).filter(Boolean);

      priceDistChart = new Chart(priceDistCanvas, {
        type: 'bar',
        data: { labels, datasets },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, beginAtZero: true },
          },
        },
      });
    }
  }

  onMount(() => {
    if (browser) loadStrategies().then(() => runBacktest());
  });

  onDestroy(() => {
    if (pnlChart) pnlChart.destroy();
    if (winlossChart) winlossChart.destroy();
    if (priceDistChart) priceDistChart.destroy();
  });
</script>

<svelte:head>
  <title>Simulated Outcomes — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="Simulated Outcomes" connected={!error} error={error || ''}>
    {#snippet children()}
      {#if loading}<Badge variant="loading" text="Running..." />{/if}
      {#if error}<Badge variant="err" text="API Error" />{/if}
      {#if lastRun > 0}<Badge variant="ok" text={`Last run: ${new Date(lastRun).toLocaleTimeString()}`} />{/if}
    {/snippet}
  </PageHeader>

  {#if error && strategies.length === 0}
    <div class="error-banner">{error}</div>
  {/if}

  <div class="controls">
    <div class="toggle-row">
      <button class="toggle-all" onclick={toggleAll}>
        {selected.size === strategies.length ? 'Deselect All' : 'Select All'}
      </button>
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
    <div class="filter-row">
      <label>Min Price:
        <input type="number" bind:value={minPrice} min="0" max="1" step="0.05" />
      </label>
      <button class="run-btn" onclick={runBacktest} disabled={loading || selected.size === 0}>
        {loading ? 'Running...' : 'Recompute All'}
      </button>
    </div>
  </div>

  {#if Object.keys(results).length > 0}
    <div class="summary-grid">
      {#each [...selected] as name}
        {@const r = results[name]}
        {#if r}
          <div class="summary-card" style="--accent: {colorFor(name)}">
            <div class="summary-header">
              <span class="dot" style="background: {colorFor(name)}"></span>
              {name}
            </div>
            <div class="summary-stats">
              <div class="stat"><span class="stat-label">Signals</span><span class="stat-val">{r.summary.total_signals}</span></div>
              <div class="stat"><span class="stat-label">Win Rate</span><span class="stat-val">{r.summary.win_rate.toFixed(1)}%</span></div>
              <div class="stat"><span class="stat-label">Net P&L</span><span class="stat-val" class:positive={r.summary.net_pnl > 0} class:negative={r.summary.net_pnl < 0}>${r.summary.net_pnl.toFixed(2)}</span></div>
              <div class="stat"><span class="stat-label">ROI</span><span class="stat-val">{r.summary.roi.toFixed(1)}%</span></div>
              <div class="stat"><span class="stat-label">Sharpe</span><span class="stat-val">{r.summary.sharpe.toFixed(2)}</span></div>
              <div class="stat"><span class="stat-label">Profit Factor</span><span class="stat-val">{r.summary.profit_factor.toFixed(2)}</span></div>
              <div class="stat"><span class="stat-label">Avg Edge</span><span class="stat-val">{r.summary.avg_edge.toFixed(1)}c</span></div>
              <div class="stat"><span class="stat-label">Max DD</span><span class="stat-val">${r.summary.max_drawdown.toFixed(2)}</span></div>
            </div>
          </div>
        {/if}
      {/each}
    </div>

    <div class="chart-section">
      <h2>Cumulative P&L</h2>
      <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={pnlCanvas}></canvas></div>
    </div>

    <div class="chart-section">
      <h2>Win / Loss Comparison</h2>
      <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={winlossCanvas}></canvas></div>
    </div>

    <div class="chart-section">
      <h2>Entry Price Distribution</h2>
      <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={priceDistCanvas}></canvas></div>
    </div>

    <div class="orders-section">
      <div class="orders-filters">
        <h2>Orders Detail</h2>
        <input type="text" placeholder="Filter by match..." bind:value={filterMatch} />
        <select bind:value={filterResult}>
          <option value="">All Results</option>
          <option value="won">Won</option>
          <option value="lost">Lost</option>
        </select>
      </div>
      {#each [...selected] as name}
        {@const r = results[name]}
        {@const filtered = r ? filterOrders(r.orders) : []}
        {#if r && filtered.length > 0}
          <CollapsibleSection title={name} count={filtered.length}>
            <table class="data-table">
              <thead><tr><th>Match</th><th>Context</th><th>Price</th><th>Edge</th><th>Size</th><th>Won</th><th>P&L</th></tr></thead>
              <tbody>
                {#each filtered.slice(0, 50) as o}
                  <tr>
                    <td class="mono">{o.match}</td><td>{o.context}</td>
                    <td>{o.price.toFixed(3)}</td><td>{o.edge_cents}c</td>
                    <td>{o.size.toFixed(1)}</td>
                    <td class={o.won ? 'pnl-win' : 'pnl-loss'}>{o.won ? 'Y' : 'N'}</td>
                    <td class={o.pnl >= 0 ? 'pnl-win' : 'pnl-loss'}>{o.pnl >= 0 ? '+' : ''}{o.pnl.toFixed(2)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
            {#if filtered.length > 50}<div class="more-rows">...and {filtered.length - 50} more</div>{/if}
          </CollapsibleSection>
        {/if}
      {/each}
    </div>
  {:else if !loading}
    <EmptyState text="No results. Select strategies and click Recompute All." />
  {/if}
</div>

<style>
  .error-banner { background: var(--loss-bg); color: var(--loss); padding: 12px 16px; border-radius: var(--radius); margin-bottom: 16px; font-size: 13px; }
  .controls { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; margin-bottom: 20px; }
  .toggle-row { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
  .toggle-all { background: var(--surface-hover); border: 1px solid var(--border-strong); color: #94a3b8; padding: 6px 12px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; }
  .toggle-all:hover { background: var(--border-strong); }
  .toggle-btn { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text-muted); padding: 6px 12px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; display: flex; align-items: center; gap: 6px; transition: all 0.15s; }
  .toggle-btn.active { border-color: var(--btn-color); color: var(--text); }
  .toggle-btn:hover { border-color: var(--btn-color); }
  .filter-row { display: flex; align-items: center; gap: 12px; }
  .filter-row label { font-size: 13px; color: #94a3b8; }
  .filter-row input { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 4px 8px; border-radius: var(--radius-xs); width: 80px; font-size: 13px; }
  .run-btn { background: #1e40af; border: 1px solid #3b82f6; color: var(--text); padding: 6px 16px; border-radius: var(--radius-sm); font-size: 13px; font-weight: 600; cursor: pointer; }
  .run-btn:hover { background: #2563eb; }
  .run-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .summary-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 12px; margin-bottom: 20px; }
  .summary-card { background: var(--surface); border: 1px solid var(--border); border-left: 3px solid var(--accent); border-radius: var(--radius); padding: 14px; }
  .summary-header { display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; color: var(--text-bright); margin-bottom: 10px; }
  .summary-stats { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
  .stat { display: flex; flex-direction: column; }
  .stat-label { font-size: 10px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; }
  .stat-val { font-size: 15px; font-weight: 700; color: var(--text-bright); }
  .stat-val.positive { color: var(--win); }
  .stat-val.negative { color: var(--loss); }
  .orders-section { margin-top: 24px; }
  .orders-section h2 { font-size: 16px; font-weight: 600; color: #cbd5e1; margin: 24px 0 12px 0; }
  .orders-filters { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
  .orders-filters h2 { margin: 0; }
  .orders-filters input { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 5px 10px; border-radius: var(--radius-xs); font-size: 13px; width: 200px; }
  .orders-filters select { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 5px 10px; border-radius: var(--radius-xs); font-size: 13px; }
  .more-rows { text-align: center; color: var(--text-muted); font-size: 12px; padding: 8px; }
</style>
