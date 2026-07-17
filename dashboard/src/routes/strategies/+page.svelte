<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';

  const API_URL = 'http://127.0.0.1:6061';

  let strategies = $state([]);
  let selected = $state(new Set());
  let results = $state({});
  let loading = $state(false);
  let error = $state(null);
  let minPrice = $state(0);
  let lastRun = $state(0);

  let pnlChart = null;
  let winlossChart = null;
  let priceDistChart = null;
  let pnlCanvas, winlossCanvas, priceDistCanvas;

  const strategyColors = {
    'matchpoint': '#60a5fa',
    'matchpoint-aggro': '#a78bfa',
    'setpoint': '#34d399',
    'setpoint-serve': '#fbbf24',
    'setpoint-cheap': '#f472b0',
    'fadelongshot': '#f87171',
  };

  function colorFor(name) {
    return strategyColors[name] || '#94a3b8';
  }

  async function loadStrategies() {
    try {
      const res = await fetch(`${API_URL}/api/strategies`);
      const data = await res.json();
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
      const names = [...selected].join(',');
      const params = new URLSearchParams({ strategies: names });
      if (minPrice > 0) params.set('min_price', String(minPrice));
      const res = await fetch(`${API_URL}/api/backtest?${params}`);
      const data = await res.json();
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

  function toggle(name) {
    const next = new Set(selected);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selected = next;
  }

  function toggleAll() {
    if (selected.size === strategies.length) selected = new Set();
    else selected = new Set(strategies);
  }

  function cumulativePnL(orders) {
    let cum = 0;
    return orders.map((o) => {
      cum += o.pnl;
      return Math.round(cum * 100) / 100;
    });
  }

  async function renderCharts() {
    if (!browser || Object.keys(results).length === 0) return;
    const mod = await import('chart.js');
    const Chart = mod.Chart;
    Chart.register(
      mod.LineController, mod.BarController, mod.LineElement, mod.BarElement,
      mod.PointElement, mod.LinearScale, mod.CategoryScale,
      mod.Filler, mod.Tooltip, mod.Legend
    );

    const selNames = [...selected];
    const hasData = selNames.some(n => results[n] && results[n].orders.length > 0);

    // --- Cumulative P&L ---
    if (pnlChart) { pnlChart.destroy(); pnlChart = null; }
    if (pnlCanvas && hasData) {
      const datasets = selNames.map(name => {
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

      const maxLen = Math.max(...datasets.map(d => d.data.length), 0);
      pnlChart = new Chart(pnlCanvas, {
        type: 'line',
        data: { labels: Array.from({ length: maxLen }, (_, i) => i + 1), datasets },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } }, tooltip: { mode: 'index', intersect: false } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Order #', color: '#64748b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 }, callback: (v) => '$' + v }, grid: { color: '#1e293b' }, title: { display: true, text: 'Cumulative P&L ($)', color: '#64748b' } },
          },
        },
      });
    }

    // --- Win/Loss bar ---
    if (winlossChart) { winlossChart.destroy(); winlossChart = null; }
    if (winlossCanvas && hasData) {
      winlossChart = new Chart(winlossCanvas, {
        type: 'bar',
        data: {
          labels: selNames,
          datasets: [
            { label: 'Wins', data: selNames.map(n => results[n]?.summary.wins || 0), backgroundColor: '#34d399' },
            { label: 'Losses', data: selNames.map(n => results[n]?.summary.losses || 0), backgroundColor: '#f87171' },
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

    // --- Price distribution ---
    if (priceDistChart) { priceDistChart.destroy(); priceDistChart = null; }
    if (priceDistCanvas && hasData) {
      const labels = Array.from({ length: 10 }, (_, i) => `${i * 10}-${(i + 1) * 10}c`);
      const datasets = selNames.map(name => {
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
  <title>Strategy Outcomes — Ghost Trader</title>
</svelte:head>

<div class="page">
  <header>
    <div class="header-left">
      <a href="/" class="back-link">← Dashboard</a>
      <h1>Strategy Outcomes</h1>
    </div>
    <div class="header-right">
      {#if loading}<span class="badge loading">Running...</span>{/if}
      {#if error}<span class="badge err" title={error}>API Error</span>{/if}
      {#if lastRun > 0}<span class="badge ok">Last run: {new Date(lastRun).toLocaleTimeString()}</span>{/if}
    </div>
  </header>

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
      <div class="chart-container"><canvas bind:this={pnlCanvas}></canvas></div>
    </div>

    <div class="chart-section">
      <h2>Win / Loss Comparison</h2>
      <div class="chart-container"><canvas bind:this={winlossCanvas}></canvas></div>
    </div>

    <div class="chart-section">
      <h2>Entry Price Distribution</h2>
      <div class="chart-container"><canvas bind:this={priceDistCanvas}></canvas></div>
    </div>

    <div class="orders-section">
      <h2>Orders Detail</h2>
      {#each [...selected] as name}
        {@const r = results[name]}
        {#if r && r.orders.length > 0}
          <div class="orders-table-wrap">
            <div class="table-title">
              <span class="dot" style="background: {colorFor(name)}"></span>
              {name} — {r.orders.length} orders
            </div>
            <table>
              <thead><tr><th>Match</th><th>Context</th><th>Price</th><th>Edge</th><th>Size</th><th>Won</th><th>P&L</th></tr></thead>
              <tbody>
                {#each r.orders.slice(0, 50) as o}
                  <tr>
                    <td class="mono">{o.match}</td><td>{o.context}</td>
                    <td>{o.price.toFixed(3)}</td><td>{o.edge_cents}c</td>
                    <td>{o.size.toFixed(1)}</td>
                    <td class={o.won ? 'win' : 'loss'}>{o.won ? 'Y' : 'N'}</td>
                    <td class={o.pnl >= 0 ? 'positive' : 'negative'}>{o.pnl >= 0 ? '+' : ''}{o.pnl.toFixed(2)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
            {#if r.orders.length > 50}<div class="more-rows">...and {r.orders.length - 50} more</div>{/if}
          </div>
        {/if}
      {/each}
    </div>
  {:else if !loading}
    <div class="empty">No results. Select strategies and click Recompute All.</div>
  {/if}
</div>

<style>
  :global(body) { background: #020617; color: #e2e8f0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; margin: 0; }
  .page { max-width: 1400px; margin: 0 auto; padding: 20px; }
  header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
  .header-left { display: flex; align-items: center; gap: 16px; }
  .back-link { color: #64748b; text-decoration: none; font-size: 13px; }
  .back-link:hover { color: #94a3b8; }
  h1 { font-size: 22px; font-weight: 700; margin: 0; color: #f1f5f9; }
  h2 { font-size: 16px; font-weight: 600; color: #cbd5e1; margin: 24px 0 12px 0; }
  .badge { padding: 4px 10px; border-radius: 4px; font-size: 12px; font-weight: 600; }
  .badge.loading { background: #1e3a5f; color: #60a5fa; }
  .badge.err { background: #450a0a; color: #f87171; }
  .badge.ok { background: #064e3b; color: #34d399; }
  .header-right { display: flex; gap: 8px; align-items: center; }
  .error-banner { background: #450a0a; color: #f87171; padding: 12px 16px; border-radius: 8px; margin-bottom: 16px; font-size: 13px; }
  .controls { background: #0f172a; border: 1px solid #1e293b; border-radius: 8px; padding: 16px; margin-bottom: 20px; }
  .toggle-row { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
  .toggle-all { background: #1e293b; border: 1px solid #334155; color: #94a3b8; padding: 6px 12px; border-radius: 6px; font-size: 12px; cursor: pointer; }
  .toggle-all:hover { background: #334155; }
  .toggle-btn { background: #1e293b; border: 1px solid #334155; color: #64748b; padding: 6px 12px; border-radius: 6px; font-size: 12px; cursor: pointer; display: flex; align-items: center; gap: 6px; transition: all 0.15s; }
  .toggle-btn.active { border-color: var(--btn-color); color: #e2e8f0; }
  .toggle-btn:hover { border-color: var(--btn-color); }
  .dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
  .filter-row { display: flex; align-items: center; gap: 12px; }
  .filter-row label { font-size: 13px; color: #94a3b8; }
  .filter-row input { background: #1e293b; border: 1px solid #334155; color: #e2e8f0; padding: 4px 8px; border-radius: 4px; width: 80px; font-size: 13px; }
  .run-btn { background: #1e40af; border: 1px solid #3b82f6; color: #e2e8f0; padding: 6px 16px; border-radius: 6px; font-size: 13px; font-weight: 600; cursor: pointer; }
  .run-btn:hover { background: #2563eb; }
  .run-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .summary-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 12px; margin-bottom: 20px; }
  .summary-card { background: #0f172a; border: 1px solid #1e293b; border-left: 3px solid var(--accent); border-radius: 8px; padding: 14px; }
  .summary-header { display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; color: #f1f5f9; margin-bottom: 10px; }
  .summary-stats { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
  .stat { display: flex; flex-direction: column; }
  .stat-label { font-size: 10px; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; }
  .stat-val { font-size: 15px; font-weight: 700; color: #f1f5f9; }
  .stat-val.positive { color: #34d399; }
  .stat-val.negative { color: #f87171; }
  .chart-section { background: #0f172a; border: 1px solid #1e293b; border-radius: 8px; padding: 16px; margin-bottom: 16px; }
  .chart-container { height: 300px; width: 100%; position: relative; }
  .orders-section { margin-top: 24px; }
  .orders-table-wrap { background: #0f172a; border: 1px solid #1e293b; border-radius: 8px; padding: 14px; margin-bottom: 12px; overflow-x: auto; }
  .table-title { display: flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 600; color: #cbd5e1; margin-bottom: 10px; }
  table { width: 100%; border-collapse: collapse; font-size: 12px; }
  th { text-align: left; padding: 6px 10px; color: #64748b; font-weight: 600; border-bottom: 1px solid #1e293b; }
  td { padding: 5px 10px; border-bottom: 1px solid #1e293b; color: #cbd5e1; }
  .mono { font-family: 'SF Mono', 'Fira Code', monospace; font-size: 11px; }
  .win { color: #34d399; font-weight: 600; }
  .loss { color: #f87171; font-weight: 600; }
  .positive { color: #34d399; }
  .negative { color: #f87171; }
  .more-rows { text-align: center; color: #64748b; font-size: 12px; padding: 8px; }
  .empty { text-align: center; color: #64748b; padding: 40px; }
</style>
