<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';
  import { setupChart } from '$lib/chart-init.js';
  import ChartLoading from '$lib/components/ChartLoading.svelte';

  let { title, labels, datasets, yLabel = '' } = $props();

  /** @type {HTMLCanvasElement | null} */ let canvas = $state(null);
  /** @type {any} */ let chart = null;
  let ready = $state(false);

  onMount(async () => {
    if (!browser) return;

    const Chart = await setupChart();
    if (!Chart || !canvas) return;

    chart = new Chart(canvas, {
      type: 'bar',
      data: { labels, datasets },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        plugins: {
          legend: { labels: { color: '#94a3b8', font: { size: 11 } } },
        },
        scales: {
          x: {
            ticks: { color: '#64748b', font: { size: 11 } },
            grid: { color: '#1e293b' },
          },
          y: {
            ticks: { color: '#64748b', font: { size: 10 } },
            grid: { color: '#1e293b' },
            beginAtZero: true,
            title: yLabel ? { display: true, text: yLabel, color: '#64748b' } : undefined,
          },
        },
      },
    });
    ready = true;
  });

  onDestroy(() => {
    if (chart) chart.destroy();
  });
</script>

<div class="chart-section">
  {#if title}<h2>{title}</h2>{/if}
  <div style="height: 300px; width: 100%; position: relative;">
    <canvas bind:this={canvas}></canvas>
    {#if !ready}<ChartLoading />{/if}
  </div>
</div>
