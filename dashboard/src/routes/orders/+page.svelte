<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { fmtTime, fmtTicker, seriesFromTicker, fmtPnL, fmtPct, vibrantColor } from '$lib/utils.js';
  import { setupChart } from '$lib/chart-init.js';
  import { browser } from '$app/environment';
  import { goto } from '$app/navigation';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import ChartLoading from '$lib/components/ChartLoading.svelte';

  const store = createPoll(() => api.getOrders(), 5000, { data: null, error: null, connected: false });

  let data = $derived($store.data);
  let loading = $derived(!$store.data && $store.connected === false && !$store.error);
  let selectedStrategies = $state(new Set());
  let minPrice = $state(0);
  let filterMatch = $state('');
  let filterResult = $state('');

  // Mirror of strategies page color map so toggles stay consistent across pages.
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

  let strategies = $derived.by(() => {
    if (!data || !data.orders) return [];
    return [...new Set(data.orders.map((/** @type {any} */ o) => o.strategy))].sort();
  });

  // Initialize selection once strategies are first available.
  $effect(() => {
    if (strategies.length > 0 && selectedStrategies.size === 0) {
      selectedStrategies = new Set(strategies);
    }
  });

  /** @type {any[]} */
  let filteredOrders = $derived.by(() => {
    if (!data || !data.orders) return [];
    return data.orders.filter((/** @type {any} */ o) => {
      if (selectedStrategies.size > 0 && !selectedStrategies.has(o.strategy)) return false;
      if (minPrice > 0 && o.market_price < minPrice) return false;
      if (filterMatch && !o.match_ticker.toLowerCase().includes(filterMatch.toLowerCase())) return false;
      if (filterResult === 'won' && !o.won) return false;
      if (filterResult === 'lost' && o.won) return false;
      if (filterResult === 'pending' && o.result) return false;
      return true;
    }).sort((/** @type {any} */ a, /** @type {any} */ b) => (b.ts || 0) - (a.ts || 0));
  });

  let settledOrders = $derived(filteredOrders.filter((/** @type {any} */ o) => o.result));
  let pendingOrders = $derived(filteredOrders.filter((/** @type {any} */ o) => !o.result));

  let filteredSummary = $derived.by(() => {
    /** @type {{ total: number, resolved: number, wins: number, losses: number, pending: number, win_rate: number, invested: number, net_pnl: number, roi: number }} */
    const s = { total: 0, resolved: 0, wins: 0, losses: 0, pending: 0, win_rate: 0, invested: 0, net_pnl: 0, roi: 0 };
    for (const o of filteredOrders) {
      s.total++;
      s.invested += o.suggested_size * o.market_price;
      if (o.result) {
        s.resolved++;
        if (o.won) { s.wins++; s.net_pnl += o.suggested_size * (1.0 - o.market_price); }
        else { s.losses++; s.net_pnl += -o.suggested_size * o.market_price; }
      } else {
        s.pending++;
      }
    }
    if (s.resolved > 0) s.win_rate = (s.wins / s.resolved) * 100;
    if (s.invested > 0) s.roi = (s.net_pnl / s.invested) * 100;
    return s;
  });

  function toggleStrategy(/** @type {string} */ name) {
    const next = new Set(selectedStrategies);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selectedStrategies = next;
  }

  function toggleAllStrategies() {
    if (selectedStrategies.size === strategies.length) selectedStrategies = new Set();
    else selectedStrategies = new Set(strategies);
  }

  // Chart refs
  /** @type {HTMLCanvasElement | null} */ let pnlCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let stratPnlCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let winlossCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let priceDistCanvas = $state(null);
  /** @type {any} */ let pnlChart = null;
  /** @type {any} */ let stratPnlChart = null;
  /** @type {any} */ let winlossChart = null;
  /** @type {any} */ let priceDistChart = null;
  let pnlReady = $state(false);
  let stratPnlReady = $state(false);
  let winlossReady = $state(false);
  let priceDistReady = $state(false);

  $effect(() => {
    if (!browser || !pnlCanvas || settledOrders.length === 0) return;
    (async () => {
      pnlReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (pnlChart) pnlChart.destroy();

      const sorted = [...settledOrders].sort((a, b) => a.ts - b.ts);
      let cum = 0;
      const cumData = sorted.map((o) => { cum += o.pnl; return Math.round(cum * 100) / 100; });

      pnlChart = new Chart(pnlCanvas, {
        type: 'line',
        data: {
          labels: sorted.map((_, i) => i + 1),
          datasets: [{
            label: 'Cumulative P&L',
            data: cumData,
            borderColor: '#60a5fa',
            backgroundColor: '#60a5fa20',
            borderWidth: 2, pointRadius: 0, tension: 0.2, fill: true,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Order #', color: '#64748b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { color: '#1e293b' }, title: { display: true, text: 'P&L ($)', color: '#64748b' } },
          },
        },
      });
      pnlReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !stratPnlCanvas || filteredOrders.length === 0) return;
    (async () => {
      stratPnlReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (stratPnlChart) stratPnlChart.destroy();

      /** @type {Record<string, number>} */
      const byStrat = {};
      for (const o of filteredOrders) {
        if (!o.result) continue;
        byStrat[o.strategy] = (byStrat[o.strategy] || 0) + o.pnl;
      }
      const labels = Object.keys(byStrat);
      const values = labels.map((k) => Math.round(byStrat[k] * 100) / 100);

      stratPnlChart = new Chart(stratPnlCanvas, {
        type: 'bar',
        data: {
          labels,
          datasets: [{
            label: 'Net P&L',
            data: values,
            backgroundColor: values.map((v) => v >= 0 ? '#34d39980' : '#f8717180'),
            borderColor: values.map((v) => v >= 0 ? '#34d399' : '#f87171'),
            borderWidth: 1,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { display: false } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { color: '#1e293b' } },
          },
        },
      });
      stratPnlReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !winlossCanvas || filteredOrders.length === 0) return;
    (async () => {
      winlossReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (winlossChart) winlossChart.destroy();

      /** @type {Record<string, {wins: number, losses: number}>} */
      const byStrat = {};
      for (const o of filteredOrders) {
        if (!o.result) continue;
        if (!byStrat[o.strategy]) byStrat[o.strategy] = { wins: 0, losses: 0 };
        if (o.won) byStrat[o.strategy].wins++;
        else byStrat[o.strategy].losses++;
      }
      const labels = Object.keys(byStrat);

      winlossChart = new Chart(winlossCanvas, {
        type: 'bar',
        data: {
          labels,
          datasets: [
            { label: 'Wins', data: labels.map((k) => byStrat[k].wins), backgroundColor: '#34d399' },
            { label: 'Losses', data: labels.map((k) => byStrat[k].losses), backgroundColor: '#f87171' },
          ],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, beginAtZero: true },
          },
        },
      });
      winlossReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !priceDistCanvas || filteredOrders.length === 0) return;
    (async () => {
      priceDistReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (priceDistChart) priceDistChart.destroy();

      const bins = new Array(10).fill(0);
      for (const o of filteredOrders) {
        const idx = Math.min(Math.floor(o.market_price * 10), 9);
        bins[idx]++;
      }

      priceDistChart = new Chart(priceDistCanvas, {
        type: 'bar',
        data: {
          labels: Array.from({ length: 10 }, (_, i) => `${i * 10}-${(i + 1) * 10}c`),
          datasets: [{
            label: 'Orders',
            data: bins,
            backgroundColor: '#60a5fa80',
            borderColor: '#60a5fa',
            borderWidth: 1,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { display: false } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Entry Price', color: '#64748b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, beginAtZero: true },
          },
        },
      });
      priceDistReady = true;
    })();
  });
</script>

<svelte:head>
  <title>Paper Orders — Ghost Trader</title>
</svelte:head>

<div class="page-container wide">
  <PageHeader title="Paper Orders" connected={$store.connected} error={$store.error || ''} />

  {#if data}
    <div class="summary-bar">
      <div class="summary-stat">
        <span class="label">Total</span>
        <span class="value">{filteredSummary.total}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Resolved</span>
        <span class="value">{filteredSummary.resolved}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Wins</span>
        <span class="value value-win">{filteredSummary.wins}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Losses</span>
        <span class="value value-loss">{filteredSummary.losses}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Pending</span>
        <span class="value">{filteredSummary.pending}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Win Rate</span>
        <span class="value">{filteredSummary.win_rate.toFixed(1)}%</span>
      </div>
      <div class="summary-stat">
        <span class="label">Invested</span>
        <span class="value">${filteredSummary.invested.toFixed(2)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Net P&L</span>
        <span class="value {filteredSummary.net_pnl >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPnL(filteredSummary.net_pnl)}
        </span>
      </div>
      <div class="summary-stat">
        <span class="label">ROI</span>
        <span class="value {filteredSummary.roi >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPct(filteredSummary.roi)}
        </span>
      </div>
    </div>
  {/if}

  {#if loading}
    <EmptyState text="Loading paper orders..." />
  {:else if $store.error}
    <EmptyState text={$store.error} variant="error" />
  {:else if !data || data.orders.length === 0}
    <EmptyState text="No paper orders yet." />
  {:else}
    <div class="layout">
      <div class="main-content">
        <div class="filter-count">{filteredOrders.length} shown ({settledOrders.length} settled, {pendingOrders.length} pending)</div>

        {#if filteredOrders.length > 0}
          <CollapsibleSection title="Analysis" count={filteredOrders.length}>
            <div class="chart-grid">
              <div class="chart-card">
                <h3>Cumulative P&L</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={pnlCanvas}></canvas>{#if !pnlReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>P&L by Strategy</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={stratPnlCanvas}></canvas>{#if !stratPnlReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>Win / Loss by Strategy</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={winlossCanvas}></canvas>{#if !winlossReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>Entry Price Distribution</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={priceDistCanvas}></canvas>{#if !priceDistReady}<ChartLoading />{/if}</div>
              </div>
            </div>
          </CollapsibleSection>
        {/if}

        {#if pendingOrders.length > 0}
          <CollapsibleSection title="Open Positions" count={pendingOrders.length}>
            <div class="table-wrap">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Match</th>
                    <th>Series</th>
                    <th>Player</th>
                    <th>Context</th>
                    <th>Strategy</th>
                    <th>Price</th>
                    <th>Edge</th>
                    <th>Size</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {#each pendingOrders as o}
                    <tr class="row-pending clickable" onclick={() => goto(`/matches/${o.match_ticker}`)}>
                      <td class="mono">{fmtTime(o.ts)}</td>
                      <td>{fmtTicker(o.match_ticker)}</td>
                      <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                      <td>{o.player_name || o.market_ticker}</td>
                      <td>{o.context}</td>
                      <td>{o.strategy}</td>
                      <td>{(o.market_price * 100).toFixed(0)}c</td>
                      <td>{o.edge_cents}c</td>
                      <td>{o.suggested_size}</td>
                      <td><Badge variant="pending" text="PENDING" /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        {/if}

        {#if settledOrders.length > 0}
          <CollapsibleSection title="Settled Trades" count={settledOrders.length}>
            <div class="table-wrap">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Match</th>
                    <th>Series</th>
                    <th>Player</th>
                    <th>Context</th>
                    <th>Strategy</th>
                    <th>Price</th>
                    <th>Edge</th>
                    <th>Size</th>
                    <th>Result</th>
                    <th>P&L</th>
                  </tr>
                </thead>
                <tbody>
                  {#each settledOrders as o}
                    <tr class={`${o.won ? 'row-win' : 'row-loss'} clickable`} onclick={() => goto(`/matches/${o.match_ticker}`)}>
                      <td class="mono">{fmtTime(o.ts)}</td>
                      <td>{fmtTicker(o.match_ticker)}</td>
                      <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                      <td>{o.player_name || o.market_ticker}</td>
                      <td>{o.context}</td>
                      <td>{o.strategy}</td>
                      <td>{(o.market_price * 100).toFixed(0)}c</td>
                      <td>{o.edge_cents}c</td>
                      <td>{o.suggested_size}</td>
                      <td>
                        <Badge variant={o.won ? 'ok' : 'err'} text={o.won ? 'WON' : 'LOST'} />
                      </td>
                      <td class={o.pnl >= 0 ? 'pnl-win' : 'pnl-loss'}>
                        {fmtPnL(o.pnl)}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        {/if}

        {#if pendingOrders.length === 0 && settledOrders.length === 0}
          <EmptyState text="No orders match current filters." />
        {/if}
      </div>

      <aside class="filter-sidebar">
        <div class="filter-group">
          <h3>Strategies</h3>
          <button class="toggle-all" onclick={toggleAllStrategies}>
            {selectedStrategies.size === strategies.length ? 'Deselect All' : 'Select All'}
          </button>
          <div class="strategy-list">
            {#each strategies as name}
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
          <h3>Filters</h3>
          <label class="filter-label">Min Price
            <input type="number" bind:value={minPrice} min="0" max="1" step="0.05" />
          </label>
          <label class="filter-label">Match
            <input type="text" placeholder="Search match..." bind:value={filterMatch} />
          </label>
          <label class="filter-label">Result
            <select bind:value={filterResult}>
              <option value="">All Results</option>
              <option value="won">Won</option>
              <option value="lost">Lost</option>
              <option value="pending">Pending</option>
            </select>
          </label>
        </div>
      </aside>
    </div>
  {/if}
</div>

<style>
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
  .filter-label input, .filter-label select { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 5px 10px; border-radius: var(--radius-xs); font-size: 13px; }
  .filter-label input { width: 100%; box-sizing: border-box; }
  .filter-count { font-size: 12px; color: var(--text-muted); margin-bottom: 16px; }
  .chart-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(380px, 1fr)); gap: 16px; }
  .chart-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 14px; }
  .chart-card h3 { font-size: 13px; font-weight: 600; color: var(--text-bright); margin: 0 0 10px; }
  .chart-container { height: 240px; position: relative; }
  .clickable { cursor: pointer; }
  .clickable:hover { background: var(--surface-hover); }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
</style>
