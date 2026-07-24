<script>
  import { browser } from '$app/environment';
  import { pushToast } from '$lib/toast.js';

  let { metrics = null, liquidityPool = null, systemMetrics = null } = $props();

  let notified = $state(new Set());

  function checkAlerts() {
    if (!browser) return;
    const alerts = [];

    if (metrics) {
      const wr = metrics.resolved > 0 ? (metrics.wins / metrics.resolved) * 100 : 100;
      if (metrics.resolved >= 10 && wr < 40) {
        alerts.push({ id: 'low_wr', type: 'err', text: `Win rate dropped to ${wr.toFixed(1)}%` });
      }
      if (metrics.net_pnl < -50) {
        alerts.push({ id: 'neg_pnl', type: 'err', text: `Net P&L at $${metrics.net_pnl.toFixed(2)}` });
      }
    }

    if (liquidityPool) {
      const balance = liquidityPool.balance_cents / 100;
      const initial = liquidityPool.initial_balance_cents / 100;
      if (initial > 0 && balance < initial * 0.2) {
        alerts.push({ id: 'low_pool', type: 'err', text: `Liquidity pool low: $${balance.toFixed(2)} (${((balance/initial)*100).toFixed(0)}% remaining)` });
      }
    }

    if (systemMetrics) {
      const numCPU = systemMetrics.num_cpu || 1;
      const totalMem = systemMetrics.total_mem_bytes || 0;

      // Goroutines: scale with CPU count. Base 200 per core.
      const goroutineThreshold = 200 * numCPU;
      if (systemMetrics.goroutines > goroutineThreshold) {
        alerts.push({ id: 'goroutine_leak', type: 'err', text: `Goroutine count high: ${systemMetrics.goroutines} (threshold ${goroutineThreshold} for ${numCPU} CPUs)` });
      }

      // Heap: warn at 50% of total system RAM.
      if (totalMem > 0) {
        const heapPct = (systemMetrics.heap_alloc_bytes / totalMem) * 100;
        if (heapPct > 50) {
          const heapMB = systemMetrics.heap_alloc_bytes / 1048576;
          const totalGB = totalMem / 1073741824;
          alerts.push({ id: 'heap_high', type: 'err', text: `Heap at ${heapPct.toFixed(0)}% of RAM: ${heapMB.toFixed(0)} MB / ${totalGB.toFixed(0)} GB` });
        }
      }

      // CPU: warn above 80% system-wide.
      if (systemMetrics.cpu_usage_pct > 80) {
        alerts.push({ id: 'cpu_high', type: 'err', text: `CPU usage high: ${systemMetrics.cpu_usage_pct.toFixed(0)}%` });
      }
    }

    for (const a of alerts) {
      if (!notified.has(a.id)) {
        pushToast(/** @type {'ok'|'err'|'info'} */ (a.type), a.text, 8000);
        notified.add(a.id);
        if (Notification?.permission === 'granted') {
          new Notification('Ghost Trader Alert', { body: a.text });
        }
      }
    }

    // Clear resolved alerts
    const activeIds = new Set(alerts.map((a) => a.id));
    for (const id of [...notified]) {
      if (!activeIds.has(id)) notified.delete(id);
    }
  }

  $effect(() => {
    if (browser && (metrics || liquidityPool || systemMetrics)) {
      checkAlerts();
    }
  });

  export async function requestNotificationPermission() {
    if (browser && Notification && Notification.permission === 'default') {
      await Notification.requestPermission();
    }
  }
</script>
