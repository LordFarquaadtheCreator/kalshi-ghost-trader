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
      if (systemMetrics.goroutines > 500) {
        alerts.push({ id: 'goroutine_leak', type: 'err', text: `Goroutine count high: ${systemMetrics.goroutines}` });
      }
      const heapMB = systemMetrics.heap_alloc_bytes / 1048576;
      if (heapMB > 500) {
        alerts.push({ id: 'heap_high', type: 'err', text: `Heap allocation high: ${heapMB.toFixed(0)} MB` });
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
