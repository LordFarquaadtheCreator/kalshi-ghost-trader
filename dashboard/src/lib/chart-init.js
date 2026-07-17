import { browser } from '$app/environment';

/** @type {Promise<any> | null} */
let chartPromise = null;

export async function setupChart() {
  if (!browser) return null;
  if (chartPromise) return chartPromise;

  chartPromise = (async () => {
    const [mod, zoomMod] = await Promise.all([
      import('chart.js'),
      import('chartjs-plugin-zoom'),
    ]);
    mod.Chart.register(
      mod.LineController, mod.BarController,
      mod.LineElement, mod.BarElement, mod.PointElement,
      mod.LinearScale, mod.CategoryScale, mod.TimeScale,
      mod.Filler, mod.Tooltip, mod.Legend,
      zoomMod.default,
    );
    return mod.Chart;
  })();

  return chartPromise;
}
