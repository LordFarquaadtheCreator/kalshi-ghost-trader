<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';
  import { setupChart } from '$lib/chart-init.js';
  import ChartLoading from '$lib/components/ChartLoading.svelte';
  import { vibrantColorByIndex } from '$lib/utils.js';

  let { title, series, store, maxY = undefined, yUnit = '' } = $props();

  /** @type {HTMLCanvasElement | null} */ let canvas = $state(null);
  /** @type {any} */ let chart = null;
  /** @type {(() => void) | null} */ let unsub = null;
  let ready = $state(false);

  const buildDatasets = () =>
      series.map((/** @type {any} */ s, /** @type {number} */ i) => ({
        label: s.label,
        data: [],
        borderColor: s.color || vibrantColorByIndex(i),
        backgroundColor: (s.color || vibrantColorByIndex(i)) + '20',
        borderWidth: 2,
        pointRadius: 0,
        tension: 0.3,
        fill: series.length === 1,
      }));

  onMount(async () => {
    if (!browser) return;

    const Chart = await setupChart();
    if (!Chart || !canvas) return;

    chart = new Chart(canvas, {
      type: 'line',
      data: { labels: [], datasets: buildDatasets() },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        plugins: {
          legend: {
            display: series.length > 1,
            labels: { color: '#94a3b8', font: { size: 11 } },
          },
          tooltip: { mode: 'index', intersect: false },
        },
        scales: {
          x: {
            ticks: {
              color: '#64748b',
              font: { size: 10 },
              maxTicksLimit: 6,
              callback: function (/** @type {any} */ _, /** @type {number} */ index) {
                const data = /** @type {any} */ (this).chart.data.labels;
                if (!data || !data[index]) return '';
                return new Date(data[index]).toLocaleTimeString('en-US', {
                  hour12: false, minute: '2-digit', second: '2-digit',
                });
              },
            },
            grid: { color: '#1e293b' },
          },
          y: {
            min: 0,
            max: maxY,
            ticks: {
              color: '#64748b',
              font: { size: 10 },
              callback: (/** @type {number} */ v) => v + yUnit,
            },
            grid: { color: '#1e293b' },
          },
        },
      },
    });

    unsub = store.subscribe((/** @type {any} */ state) => {
      if (!chart || !state.history.length) return;
      chart.data.labels = state.history.map((/** @type {any} */ s) => s.timestamp);
      series.forEach((/** @type {any} */ s, /** @type {number} */ i) => {
        chart.data.datasets[i].data = state.history.map((/** @type {any} */ sample) => s.getValue(sample));
      });
      chart.update('none');
    });
    ready = true;
  });

  onDestroy(() => {
    if (unsub) unsub();
    if (chart) chart.destroy();
  });
</script>

<div class="chart-card">
  <div class="chart-title">{title}</div>
  <div class="chart-canvas-wrap" style="position: relative;">
    <canvas bind:this={canvas}></canvas>
    {#if !ready}<ChartLoading />{/if}
  </div>
</div>
