import { browser } from '$app/environment';

/** @type {Promise<any> | null} */
let chartPromise = null;

export async function setupChart() {
  if (!browser) return null;
  if (chartPromise) return chartPromise;

  chartPromise = (async () => {
    const mod = await import('chart.js');
    mod.Chart.register(
      mod.LineController, mod.BarController,
      mod.LineElement, mod.BarElement, mod.PointElement,
      mod.LinearScale, mod.CategoryScale, mod.TimeScale,
      mod.Filler, mod.Tooltip, mod.Legend,
    );
    return mod.Chart;
  })();

  return chartPromise;
}
