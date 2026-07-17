<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';
  import { page } from '$app/state';
  import { api } from '$lib/api.js';
  import { setupChart } from '$lib/chart-init.js';
  import { fmtTime } from '$lib/utils.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import ChartLoading from '$lib/components/ChartLoading.svelte';

  const POLL_MS = 3000;

  /** @type {{event_ticker: string, title: string, markets: {market_ticker: string, player_name: string, ticks: {ts: number, price: number}[]}[], orders: {ts: number, market_ticker: string, player_name: string, context: string, market_price: number, edge_cents: number, suggested_size: number, strategy: string}[]} | null} */
  let data = $state(null);
  let loading = $state(true);
  let chartReady = $state(false);
  let error = $state('');
  /** @type {ReturnType<typeof setInterval> | null} */
  let timer = null;
  /** @type {any} */
  let chart = null;
  /** @type {HTMLCanvasElement | null} */
  let canvas = $state(null);

  const eventTicker = $derived(/** @type {string} */ (page.params.event_ticker));

  async function loadTicks() {
    try {
      chartReady = false;
      data = await api.getTicks(eventTicker);
      loading = false;
      error = '';
      await renderChart();
      chartReady = true;
    } catch (err) {
      loading = false;
      error = err instanceof Error ? err.message : String(err);
    }
  }

  async function renderChart() {
    if (!browser || !data || !data.markets || data.markets.length === 0 || !canvas) return;

    const Chart = await setupChart();
    if (!Chart) return;

    if (chart) { chart.destroy(); chart = null; }

    const colors = ['#60a5fa', '#f472b0'];
    const datasets = data.markets.map((m, i) => ({
      label: m.player_name || m.market_ticker,
      data: m.ticks.map((t) => ({ x: t.ts, y: t.price })),
      borderColor: colors[i % colors.length],
      backgroundColor: colors[i % colors.length] + '20',
      borderWidth: 2,
      pointRadius: 0,
      tension: 0.2,
    }));

    if (data.orders && data.orders.length > 0) {
      const orderTimes = data.orders.map((o) => o.ts);

      const allOrderLines = {
        id: 'orderLines',
        afterDatasetsDraw(/** @type {any} */ chart) {
          const { ctx, chartArea, scales } = chart;
          if (!chartArea) return;
          ctx.save();
          ctx.strokeStyle = '#fb923c';
          ctx.lineWidth = 1.5;
          ctx.setLineDash([4, 3]);
          for (const t of orderTimes) {
            const x = scales.x.getPixelForValue(t);
            if (x >= chartArea.left && x <= chartArea.right) {
              ctx.beginPath();
              ctx.moveTo(x, chartArea.top);
              ctx.lineTo(x, chartArea.bottom);
              ctx.stroke();
            }
          }
          ctx.restore();
        },
      };

      // placeholder dataset so plugin has access — invisible point at each order
      datasets.push(/** @type {any} */ ({
        label: 'Orders',
        data: orderTimes.map((t) => ({ x: t, y: null })),
        showLine: false,
        pointRadius: 0,
        orderLines: true,
      }));

      chart = new Chart(canvas, {
        type: 'line',
        data: { datasets },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          animation: false,
          plugins: {
            legend: { labels: { color: '#94a3b8', font: { size: 12 } } },
            tooltip: {
              mode: 'index',
              intersect: false,
              callbacks: {
                label: (/** @type {any} */ ctx) => {
                  const raw = /** @type {{x: number, y: number}} */ (ctx.raw);
                  if (raw && typeof raw === 'object' && 'y' in raw && raw.y !== null) {
                    return `${ctx.dataset.label}: ${(raw.y * 100).toFixed(1)}c`;
                  }
                  return null;
                },
              },
            },
          },
          scales: {
            x: {
              type: 'linear',
              ticks: {
                color: '#64748b',
                font: { size: 10 },
                callback: (/** @type {number} */ v) => {
                  const d = new Date(v);
                  return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                },
              },
              grid: { color: '#1e293b' },
              title: { display: true, text: 'Time', color: '#64748b' },
            },
            y: {
              min: 0,
              max: 1,
              ticks: {
                color: '#64748b',
                font: { size: 10 },
                callback: (/** @type {number} */ v) => {
                  const val = Number(v);
                  return val.toFixed(2);
                },
              },
              grid: { color: '#1e293b' },
              title: { display: true, text: 'Price (cents)', color: '#64748b' },
            },
          },
        },
        plugins: [allOrderLines],
      });
    } else {
      chart = new Chart(canvas, {
        type: 'line',
        data: { datasets },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          animation: false,
          plugins: {
            legend: { labels: { color: '#94a3b8', font: { size: 12 } } },
            tooltip: {
              mode: 'index',
              intersect: false,
              callbacks: {
                label: (/** @type {any} */ ctx) => {
                  const raw = /** @type {{x: number, y: number}} */ (ctx.raw);
                  if (raw && typeof raw === 'object' && 'y' in raw) {
                    return `${ctx.dataset.label}: ${(raw.y * 100).toFixed(1)}c`;
                  }
                  return `${ctx.dataset.label}: ${ctx.raw}`;
                },
              },
            },
          },
          scales: {
            x: {
              type: 'linear',
              ticks: {
                color: '#64748b',
                font: { size: 10 },
                callback: (/** @type {number} */ v) => {
                  const d = new Date(v);
                  return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                },
              },
              grid: { color: '#1e293b' },
              title: { display: true, text: 'Time', color: '#64748b' },
            },
            y: {
              min: 0,
              max: 1,
              ticks: {
                color: '#64748b',
                font: { size: 10 },
                callback: (/** @type {number} */ v) => {
                  const val = Number(v);
                  return val.toFixed(2);
                },
              },
              grid: { color: '#1e293b' },
              title: { display: true, text: 'Price (cents)', color: '#64748b' },
            },
          },
        },
      });
    }
  }

  onMount(() => {
    if (browser) {
      loadTicks();
      timer = setInterval(loadTicks, POLL_MS);
    }
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
    if (chart) chart.destroy();
  });
</script>

<svelte:head>
  <title>{eventTicker} — Match Detail</title>
</svelte:head>

<div class="page-container">
  <PageHeader title={data?.title || eventTicker} connected={!error && !loading} error={error}>
    {#snippet children()}
      <span class="ticker">{eventTicker}</span>
    {/snippet}
  </PageHeader>

  {#if loading}
    <EmptyState text="Loading tick data..." />
  {:else if error}
    <EmptyState text={error} variant="error" />
  {:else if !data || !data.markets || data.markets.length === 0}
    <EmptyState text="No tick data for this event." />
  {:else}
    <div class="chart-container">
      {#if !chartReady}
        <ChartLoading />
      {:else}
        <canvas bind:this={canvas}></canvas>
      {/if}
    </div>

    <div class="markets-grid">
      {#each data.markets as m}
        <div class="market-card">
          <div class="market-name">{m.player_name || m.market_ticker}</div>
          <div class="market-ticker">{m.market_ticker}</div>
          <div class="market-stats">
            <span class="stat">{m.ticks.length} ticks</span>
            {#if m.ticks.length > 0}
              <span class="stat">last: {(Number(m.ticks[m.ticks.length - 1].price) * 100).toFixed(1)}c</span>
            {/if}
          </div>
        </div>
      {/each}
    </div>

    {#if data.orders && data.orders.length > 0}
      <CollapsibleSection title="Simulated Orders" count={data.orders.length}>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Player</th>
                <th>Context</th>
                <th>Price</th>
                <th>Edge</th>
                <th>Size</th>
                <th>Strategy</th>
              </tr>
            </thead>
            <tbody>
              {#each data.orders as o}
                <tr>
                  <td class="mono">{fmtTime(o.ts)}</td>
                  <td>{o.player_name || o.market_ticker}</td>
                  <td>{o.context}</td>
                  <td>{(o.market_price * 100).toFixed(0)}c</td>
                  <td>{o.edge_cents}c</td>
                  <td>{o.suggested_size}</td>
                  <td>{o.strategy}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </CollapsibleSection>
    {/if}

    <CollapsibleSection title="Real Orders" count={0} defaultOpen={false}>
      <EmptyState text="No real orders yet." />
    </CollapsibleSection>
  {/if}
</div>

<style>
  .ticker { font-family: 'SF Mono', 'Fira Code', monospace; font-size: 12px; color: var(--text-muted); }
  .chart-container { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; height: 500px; margin-bottom: 20px; }
  .markets-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 12px; }
  .market-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 14px; }
  .market-name { font-size: 15px; font-weight: 600; color: var(--text-bright); }
  .market-ticker { font-family: 'SF Mono', 'Fira Code', monospace; font-size: 11px; color: var(--text-muted); margin-top: 2px; }
  .market-stats { display: flex; gap: 12px; margin-top: 8px; }
  .market-stats .stat { font-size: 12px; color: #94a3b8; background: var(--surface-hover); padding: 3px 8px; border-radius: var(--radius-xs); }
</style>
