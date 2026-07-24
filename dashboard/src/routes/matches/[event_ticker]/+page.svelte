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
  // Auto-follow window: show last N seconds of data. 0 = show all.
  const FOLLOW_WINDOW_MS = 120_000; // 2 min

  /** @type {{event_ticker: string, title: string, markets: {market_ticker: string, player_name: string, ticks: {ts: number, price: number}[]}[], orders: {ts: number, market_ticker: string, player_name: string, context: string, market_price: number, edge_cents: number, suggested_size: number, strategy: string}[], scores: {ts: number, set_number: number, game_number: number, home_games: number, away_games: number, home_points: string, away_points: string, home_set_games: number, away_set_games: number}[]} | null} */
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

  // User interaction state — pause auto-follow when user zooms/pans
  let autoFollow = $state(true);

  const eventTicker = $derived(/** @type {string} */ (page.params.event_ticker));

  async function loadTicks() {
    try {
      data = await api.getTicks(eventTicker);
      loading = false;
      error = '';
      await updateChart();
      chartReady = true;
    } catch (err) {
      loading = false;
      error = err instanceof Error ? err.message : String(err);
    }
  }

  // Build chart once, then update data in-place on subsequent polls.
  async function ensureChart() {
    if (chart || !browser || !canvas) return null;
    const Chart = await setupChart();
    if (!Chart) return null;

    const colors = ['#60a5fa', '#f472b0'];

    const plugins = [
      {
        id: 'scoreLines',
        afterDatasetsDraw(/** @type {any} */ c) {
          if (!c.chartArea || !data?.scores) return;
          const { ctx, chartArea, scales } = c;
          ctx.save();
          for (const s of data.scores) {
            const x = scales.x.getPixelForValue(s.ts);
            if (x < chartArea.left || x > chartArea.right) continue;
            ctx.strokeStyle = '#34d39988';
            ctx.lineWidth = 1;
            ctx.setLineDash([2, 2]);
            ctx.beginPath();
            ctx.moveTo(x, chartArea.top);
            ctx.lineTo(x, chartArea.bottom);
            ctx.stroke();
            ctx.setLineDash([]);
            ctx.fillStyle = '#34d399';
            ctx.font = '10px monospace';
            const label = `${s.home_set_games}-${s.away_set_games} ${s.home_games}-${s.away_games}`;
            ctx.fillText(label, x + 3, chartArea.top + 12);
          }
          ctx.restore();
        },
      },
      {
        id: 'orderLines',
        afterDatasetsDraw(/** @type {any} */ c) {
          if (!c.chartArea || !data?.orders) return;
          const { ctx, chartArea, scales } = c;
          ctx.save();
          ctx.strokeStyle = '#fb923c';
          ctx.lineWidth = 1.5;
          ctx.setLineDash([4, 3]);
          for (const o of data.orders) {
            const x = scales.x.getPixelForValue(o.ts);
            if (x >= chartArea.left && x <= chartArea.right) {
              ctx.beginPath();
              ctx.moveTo(x, chartArea.top);
              ctx.lineTo(x, chartArea.bottom);
              ctx.stroke();
            }
          }
          ctx.restore();
        },
      },
    ];

    chart = new Chart(canvas, {
      type: 'line',
      data: {
        datasets: [
          // Market lines created in updateChart
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        parsing: false,
        normalized: true,
        plugins: {
          legend: { labels: { color: '#94a3b8', font: { size: 12 } } },
          tooltip: {
            mode: 'index',
            intersect: false,
            callbacks: {
              label: (/** @type {any} */ ctx) => {
                const raw = ctx.raw;
                if (raw && typeof raw === 'object' && 'y' in raw && raw.y !== null) {
                  return `${ctx.dataset.label}: ${(raw.y * 100).toFixed(1)}c`;
                }
                return null;
              },
            },
          },
          zoom: {
            zoom: {
              wheel: { enabled: true, speed: 0.1 },
              pinch: { enabled: true },
              drag: { enabled: true, modifierKey: 'shift' },
              mode: 'x',
              onZoom: () => { autoFollow = false; },
            },
            pan: {
              enabled: true,
              mode: 'x',
              onPan: () => { autoFollow = false; },
            },
            limits: {
              x: { min: 'original', max: 'original' },
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
              callback: (/** @type {number} */ v) => Number(v).toFixed(2),
            },
            grid: { color: '#1e293b' },
            title: { display: true, text: 'Price (cents)', color: '#64748b' },
          },
        },
      },
      plugins,
    });

    return chart;
  }

  // Update chart data in-place — no destroy/recreate, no flashing.
  async function updateChart() {
    if (!browser || !data || !data.markets || data.markets.length === 0) return;

    const c = await ensureChart();
    if (!c) return;

    const colors = ['#60a5fa', '#f472b0'];

    // Sync dataset count with market count
    while (c.data.datasets.length > data.markets.length) {
      c.data.datasets.pop();
    }
    while (c.data.datasets.length < data.markets.length) {
      c.data.datasets.push({
        label: '', data: [],
        borderColor: '#60a5fa', backgroundColor: '#60a5fa20',
        borderWidth: 2, pointRadius: 0, tension: 0.2,
      });
    }

    // Update each dataset's data in-place
    data.markets.forEach((m, i) => {
      const ds = c.data.datasets[i];
      ds.label = m.player_name || m.market_ticker;
      ds.data = m.ticks.map((t) => ({ x: t.ts, y: t.price }));
      ds.borderColor = colors[i % colors.length];
      ds.backgroundColor = colors[i % colors.length] + '20';
    });

    // Auto-follow: zoom to most recent window
    if (autoFollow) {
      applyAutoFollow(c);
    }

    c.update('none');
  }

  // Set x-axis min/max to show the most recent FOLLOW_WINDOW_MS of data.
  function applyAutoFollow(/** @type {any} */ c) {
    if (!data || data.markets.length === 0) return;
    let maxTs = 0;
    let minTs = Infinity;
    for (const m of data.markets) {
      for (const t of m.ticks) {
        if (t.ts > maxTs) maxTs = t.ts;
        if (t.ts < minTs) minTs = t.ts;
      }
    }
    if (maxTs === 0) return;

    const span = maxTs - minTs;
    if (span <= FOLLOW_WINDOW_MS) {
      // All data fits — show everything
      c.options.scales.x.min = minTs;
      c.options.scales.x.max = maxTs;
    } else {
      c.options.scales.x.min = maxTs - FOLLOW_WINDOW_MS;
      c.options.scales.x.max = maxTs;
    }
  }

  function resetZoom() {
    if (!chart) return;
    autoFollow = true;
    chart.resetZoom('none');
    applyAutoFollow(chart);
    chart.update('none');
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
    <div class="chart-container" style="position: relative;">
      <div class="chart-controls">
        <button class="zoom-btn" class:active={autoFollow} onclick={resetZoom}>
          {autoFollow ? 'Following (2min)' : 'Follow Recent'}
        </button>
        <span class="zoom-hint">scroll=zoom · drag=pan · shift+drag=box zoom</span>
      </div>
      <canvas bind:this={canvas}></canvas>
      {#if !chartReady}<ChartLoading />{/if}
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
                <th>Market</th>
                <th>Strategy</th>
                <th class="num">Price</th>
                <th class="num">Edge</th>
                <th class="num">Size</th>
                <th>Signal Context</th>
              </tr>
            </thead>
            <tbody>
              {#each [...(data.orders || [])].sort((a, b) => (b.ts || 0) - (a.ts || 0)) as o}
                <tr>
                  <td class="mono">{fmtTime(o.ts)}</td>
                  <td>{o.player_name || o.market_ticker}</td>
                  <td class="mono small">{o.market_ticker}</td>
                  <td>{o.strategy}</td>
                  <td class="num">{(o.market_price * 100).toFixed(0)}c</td>
                  <td class="num" class:edge-good={o.edge_cents > 5} class:edge-low={o.edge_cents <= 2}>{o.edge_cents}c</td>
                  <td class="num">{o.suggested_size?.toFixed(4)}</td>
                  <td class="context-cell" title={o.context}>{o.context}</td>
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
  .chart-controls { display: flex; align-items: center; gap: 12px; margin-bottom: 8px; }
  .zoom-btn { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text-muted); padding: 4px 10px; border-radius: var(--radius-xs); font-size: 11px; cursor: pointer; }
  .zoom-btn.active { border-color: #3b82f6; color: #60a5fa; }
  .zoom-btn:hover { border-color: #3b82f6; }
  .zoom-hint { font-size: 10px; color: var(--text-muted); opacity: 0.6; }
  .markets-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 12px; }
  .market-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 14px; }
  .market-name { font-size: 15px; font-weight: 600; color: var(--text-bright); }
  .market-ticker { font-family: 'SF Mono', 'Fira Code', monospace; font-size: 11px; color: var(--text-muted); margin-top: 2px; }
  .market-stats { display: flex; gap: 12px; margin-top: 8px; }
  .market-stats .stat { font-size: 12px; color: #94a3b8; background: var(--surface-hover); padding: 3px 8px; border-radius: var(--radius-xs); }
  .small { font-size: 10px; }
  .edge-good { color: var(--win); font-weight: 600; }
  .edge-low { color: var(--loss); }
  .context-cell { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; color: var(--text-muted); }
</style>
