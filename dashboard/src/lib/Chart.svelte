<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';

  let { title, series, store, maxY = undefined, yUnit = '' } = $props();

  let canvas;
  let chart;
  let unsub;

  const colors = ['#60a5fa', '#f472b0', '#34d399', '#fbbf24', '#a78bfa'];

  onMount(async () => {
    if (!browser) return;

    const { Chart, LineController, LineElement, PointElement, LinearScale, CategoryScale, TimeScale, Filler, Tooltip, Legend } = await import('chart.js');
    Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, TimeScale, Filler, Tooltip, Legend);

    const buildDatasets = () =>
      series.map((s, i) => ({
        label: s.label,
        data: [],
        borderColor: s.color || colors[i % colors.length],
        backgroundColor: (s.color || colors[i % colors.length]) + '20',
        borderWidth: 2,
        pointRadius: 0,
        tension: 0.3,
        fill: series.length === 1,
      }));

    const ctx = canvas.getContext('2d');
    chart = new Chart(ctx, {
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
              callback: function (_, index) {
                const data = this.chart.data.labels;
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
              callback: (v) => v + yUnit,
            },
            grid: { color: '#1e293b' },
          },
        },
      },
    });

    unsub = store.subscribe((state) => {
      if (!chart || !state.history.length) return;
      chart.data.labels = state.history.map((s) => s.timestamp);
      series.forEach((s, i) => {
        chart.data.datasets[i].data = state.history.map((sample) => s.getValue(sample));
      });
      chart.update('none');
    });
  });

  onDestroy(() => {
    if (unsub) unsub();
    if (chart) chart.destroy();
  });
</script>

<div class="chart-card">
  <div class="chart-title">{title}</div>
  <div class="chart-canvas-wrap">
    <canvas bind:this={canvas}></canvas>
  </div>
</div>

<style>
  .chart-card {
    background: #0f172a;
    border: 1px solid #1e293b;
    border-radius: 8px;
    padding: 12px;
  }
  .chart-title {
    color: #e2e8f0;
    font-size: 13px;
    font-weight: 600;
    margin-bottom: 8px;
  }
  .chart-canvas-wrap {
    height: 160px;
    width: 100%;
    position: relative;
  }
</style>
