<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { pushToast } from '$lib/toast.js';
  import { fmtTicker, seriesFromTicker } from '$lib/utils.js';
  import {
    dailyPnLSeries, maxDrawdown, sharpeDaily, sortinoDaily, dailyAvgPnL,
    profitFactor, mean, stdDev,
  } from '$lib/stats.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import Drawer from '$lib/components/Drawer.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import Toaster from '$lib/components/Toaster.svelte';
  import RiskDashboard from '$lib/components/RiskDashboard.svelte';
  import DateRangePicker from '$lib/components/DateRangePicker.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import PoolHistory from '$lib/components/PoolHistory.svelte';
  import { exportCSV } from '$lib/csv.js';

  // --- Polling ---
  const ordersStore = createPoll(() => api.getRealOrders(), 5000, { data: null, error: null, connected: false });
  const poolStore = createPoll(() => api.getLiquidityPool(), 5000, { data: null, error: null, connected: false });

  let ordersData = $derived($ordersStore.data);
  let poolData = $derived($poolStore.data);
  let connected = $derived($ordersStore.connected);
  let error = $derived($ordersStore.error);
  let lastFetch = $state(Date.now());
  $effect(() => {
    if (ordersData) lastFetch = Date.now();
  });

  // --- Orders ---
  /** @type {any[]} */
  let orders = $derived(ordersData?.orders ?? []);
  /** @type {{ total: number, by_strategy: Record<string, number> }} */
  let positionPnL = $derived(ordersData?.position_pnl ?? { total: 0, by_strategy: {} });
  let pool = $derived(poolData);

  // --- Tab state ---
  let activeTab = $state('orders');

  // --- Orders tab: filters ---
  let search = $state('');
  /** @type {Set<string>} */
  let statusFilter = $state(new Set());
  let strategyFilter = $state('');

  const ALL_STATUSES = ['submitted', 'filled', 'partial', 'resolved', 'failed', 'canceled'];

  let statusCounts = $derived.by(() => {
    /** @type {Record<string, number>} */
    const counts = {};
    for (const o of orders) {
      const s = o.OrderStatus || 'pending';
      counts[s] = (counts[s] || 0) + 1;
    }
    return counts;
  });

  let strategies = $derived.by(() => {
    /** @type {string[]} */
    const out = [];
    const seen = new Set();
    for (const o of orders) {
      if (o.Strategy && !seen.has(o.Strategy)) {
        seen.add(o.Strategy);
        out.push(o.Strategy);
      }
    }
    out.sort();
    return out;
  });

  function toggleStatus(/** @type {string} */ s) {
    const next = new Set(statusFilter);
    if (next.has(s)) next.delete(s);
    else next.add(s);
    statusFilter = next;
    page = 0;
  }

  function clearStatus() {
    statusFilter = new Set();
    page = 0;
  }

  // --- Sort ---
  let sortKey = $state('TS');
  let sortDir = $state('desc');

  /** @param {string} key */
  function toggleSort(key) {
    if (sortKey === key) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortKey = key;
      sortDir = 'desc';
    }
  }

  // --- Filtered + sorted orders ---
  let filteredOrders = $derived.by(() => {
    const q = search.trim().toLowerCase();
    let out = orders.filter((/** @type {any} */ o) => {
      if (statusFilter.size > 0 && !statusFilter.has(o.OrderStatus || 'pending')) return false;
      if (strategyFilter && o.Strategy !== strategyFilter) return false;
      if (q) {
        const hay = [
          o.MatchTitle, o.MatchTicker, o.PlayerName, o.MarketTicker,
          o.Strategy, o.KalshiOrderID, o.Context,
        ].filter(Boolean).join(' ').toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
    const dir = sortDir === 'asc' ? 1 : -1;
    out = [...out].sort((a, b) => {
      let av, bv;
      if (sortKey === '_roi') {
        av = orderROI(a) ?? -Infinity;
        bv = orderROI(b) ?? -Infinity;
      } else {
        av = a[sortKey];
        bv = b[sortKey];
      }
      if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * dir;
      return String(av ?? '').localeCompare(String(bv ?? '')) * dir;
    });
    return out;
  });

  // --- Pagination ---
  const PAGE_SIZE = 50;
  let page = $state(0);
  let totalPages = $derived(Math.max(1, Math.ceil(filteredOrders.length / PAGE_SIZE)));
  let pageOrders = $derived(filteredOrders.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE));

  $effect(() => {
    if (page >= totalPages) page = totalPages - 1;
  });

  // --- Open positions (buys not yet fully closed by sells) ---
  // Build position fill map from all orders: position_id -> {buy, sell}
  // A buy is "open" only if its position's sell fill < buy fill.
  let openOrders = $derived.by(() => {
    /** @type {Record<number, {buy: number, sell: number}>} */
    const posFills = {};
    for (const o of orders) {
      if (!o.PositionID) continue;
      const pid = o.PositionID;
      if (!posFills[pid]) posFills[pid] = { buy: 0, sell: 0 };
      if (o.Side === 'close') {
        posFills[pid].sell += o.FillCount || 0;
      } else {
        posFills[pid].buy += o.FillCount || 0;
      }
    }
    return orders
      .filter((/** @type {any} */ o) => {
        if (!['submitted', 'filled', 'partial'].includes(o.OrderStatus)) return false;
        // Sells are closing actions, not open positions
        if (o.Side === 'close') return false;
        // Buy with a position: check if position is fully closed
        if (o.PositionID && posFills[o.PositionID] && posFills[o.PositionID].sell >= posFills[o.PositionID].buy) {
          return false;
        }
        return true;
      })
      .sort((a, b) => (b.TS || 0) - (a.TS || 0));
  });
  let exposureCents = $derived.by(() => {
    let sum = 0;
    for (const o of openOrders) {
      sum += Math.round((o.FillCount || 0) * (o.MarketPrice || 0) * 100);
    }
    return sum;
  });

  // --- Comprehensive metrics (computed client-side from all orders) ---
  let resolvedOrders = $derived(orders.filter((o) => o.OrderStatus === 'resolved'));
  let settledPnls = $derived(resolvedOrders.map((o) => o.ResolvedPNLCents || 0));
  let wins = $derived(resolvedOrders.filter((o) => (o.ResolvedPNLCents || 0) > 0).length);
  let losses = $derived(resolvedOrders.filter((o) => (o.ResolvedPNLCents || 0) < 0).length);
  let investedCents = $derived.by(() => {
    return orders.reduce((s, o) => s + Math.round((o.FillCount || 0) * (o.MarketPrice || 0) * 100), 0);
  });
  let dailySeries = $derived(dailyPnLSeries(orders));

  // Per-strategy computed metrics (from orders array, not metrics API)
  let strategyMetrics = $derived.by(() => {
    /** @type {Record<string, any[]>} */
    const byStrat = {};
    for (const o of orders) {
      const s = o.Strategy || 'unknown';
      if (!byStrat[s]) byStrat[s] = [];
      byStrat[s].push(o);
    }
    /** @type {Record<string, any>} */
    const out = {};
    for (const [s, os] of Object.entries(byStrat)) {
      const resolved = os.filter((o) => o.OrderStatus === 'resolved');
      const pnls = resolved.map((o) => o.ResolvedPNLCents || 0);
      const w = pnls.filter((p) => p > 0).length;
      const l = pnls.filter((p) => p < 0).length;
      const netPnl = pnls.reduce((a, b) => a + b, 0) + (positionPnL.by_strategy[s] || 0);
      const inv = os.reduce((s2, o) => s2 + Math.round((o.FillCount || 0) * (o.MarketPrice || 0) * 100), 0);
      const series = dailyPnLSeries(os);
      out[s] = {
        total: os.length,
        resolved: resolved.length,
        wins: w,
        losses: l,
        winRate: resolved.length > 0 ? (w / resolved.length) * 100 : 0,
        netPnl,
        invested: inv,
        roi: inv > 0 ? (netPnl / inv) * 100 : 0,
        dailyAvg: dailyAvgPnL(series),
        sharpe: sharpeDaily(series),
        sortino: sortinoDaily(series),
        maxDD: maxDrawdown(series),
        profitFactor: profitFactor(pnls),
        avgPnl: pnls.length > 0 ? mean(pnls) : 0,
        pnlStd: stdDev(pnls),
        days: series.length,
      };
    }
    return out;
  });

  // --- Summary KPIs ---
  let summary = $derived.by(() => {
    const total = orders.length;
    const filled = orders.filter((o) => o.OrderStatus === 'filled' || o.OrderStatus === 'partial').length;
    const resolved = resolvedOrders.length;
    const failed = orders.filter((o) => o.OrderStatus === 'failed').length;
    const pnl = settledPnls.reduce((a, b) => a + b, 0) + (positionPnL.total || 0);
    const wr = resolved > 0 ? (wins / resolved) * 100 : 0;
    const roi = investedCents > 0 ? (pnl / investedCents) * 100 : 0;
    const dAvg = dailyAvgPnL(dailySeries);
    const sh = sharpeDaily(dailySeries);
    const so = sortinoDaily(dailySeries);
    const pf = profitFactor(settledPnls);
    const mdd = maxDrawdown(dailySeries);
    const avgPnl = settledPnls.length > 0 ? mean(settledPnls) : 0;
    const pnlStd = stdDev(settledPnls);
    return {
      total, filled, resolved, failed, pnl,
      wins, losses, wr, roi, investedCents,
      dAvg, sh, so, pf, mdd, avgPnl, pnlStd,
      days: dailySeries.length,
    };
  });

  // --- Per-order ROI ---
  function orderROI(/** @type {any} */ o) {
    const inv = (o.FillCount || 0) * (o.MarketPrice || 0) * 100;
    if (!inv) return null;
    return ((o.ResolvedPNLCents || 0) / inv) * 100;
  }

  // --- Drawer ---
  /** @type {any} */
  let selectedOrder = $state(null);
  let drawerOpen = $derived(selectedOrder !== null);

  function openDrawer(/** @type {any} */ o) {
    selectedOrder = o;
  }
  function closeDrawer() {
    selectedOrder = null;
  }

  // --- Pool modal ---
  let poolModalOpen = $state(false);
  let resetDollars = $state('');
  let resetConfirm = $state('');
  let topupDollars = $state('');
  let poolBusy = $state(false);

  async function handleReset() {
    const dollars = parseFloat(resetDollars);
    if (!dollars || dollars <= 0) {
      pushToast('err', 'Enter a positive dollar amount');
      return;
    }
    if (resetConfirm !== resetDollars) {
      pushToast('err', 'Type the exact amount to confirm');
      return;
    }
    poolBusy = true;
    try {
      await api.resetLiquidityPool(Math.round(dollars * 100));
      pushToast('ok', `Pool reset to $${dollars.toFixed(2)}`);
      resetDollars = '';
      resetConfirm = '';
    } catch (/** @type {any} */ e) {
      pushToast('err', `Reset failed: ${e.message}`);
    } finally {
      poolBusy = false;
    }
  }

  async function handleTopUp() {
    const dollars = parseFloat(topupDollars);
    if (!dollars || dollars <= 0) {
      pushToast('err', 'Enter a positive dollar amount');
      return;
    }
    poolBusy = true;
    try {
      await api.topUpLiquidityPool(Math.round(dollars * 100));
      pushToast('ok', `Topped up $${dollars.toFixed(2)}`);
      topupDollars = '';
    } catch (/** @type {any} */ e) {
      pushToast('err', `Top-up failed: ${e.message}`);
    } finally {
      poolBusy = false;
    }
  }

  // --- Helpers ---
  /** @param {number} c */
  function fmtCents(c) {
    if (!c) return '$0.00';
    const sign = c < 0 ? '-' : '';
    return `${sign}$${(Math.abs(c) / 100).toFixed(2)}`;
  }
  /** @param {number} ts */
  function fmtTimeShort(ts) {
    if (!ts) return '';
    return new Date(ts).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }
  /** @param {number} ts */
  function fmtDate(ts) {
    if (!ts) return '';
    return new Date(ts).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }
  /** @param {any} m */
  function winRate(m) {
    if (!m || !m.resolved) return '—';
    return `${((m.wins / m.resolved) * 100).toFixed(1)}%`;
  }
  /** @param {any} m */
  function roi(m) {
    if (!m || !m.total_invested) return '—';
    return `${((m.net_pnl_cents / 100 / m.total_invested) * 100).toFixed(1)}%`;
  }
  /** @param {number} v — percentage value */
  function fmtPct(v) {
    if (v === null || v === undefined || isNaN(v)) return '—';
    const sign = v > 0 ? '+' : '';
    return `${sign}${v.toFixed(1)}%`;
  }
  /** @param {number} v — ratio (sharpe, sortino, profit factor) */
  function fmtRatio(v) {
    if (v === null || v === undefined || isNaN(v)) return '—';
    if (v === Infinity) return '∞';
    return v.toFixed(2);
  }
  /** @param {number} v — cents */
  function fmtSignedCents(v) {
    if (!v) return '$0.00';
    const sign = v < 0 ? '-' : '+';
    return `${sign}$${(Math.abs(v) / 100).toFixed(2)}`;
  }
  /** @param {string} s */
  function statusVariant(s) {
    if (s === 'filled' || s === 'resolved') return 'ok';
    if (s === 'failed') return 'err';
    if (s === 'submitted' || s === 'partial') return 'loading';
    if (s === 'canceled') return 'pending';
    return 'pending';
  }
  /** @param {string} a */
  function sideLabel(a) {
    if (a === 'buy') return 'BUY YES';
    if (a === 'buy_no') return 'BUY NO';
    if (a === 'sell') return 'SELL';
    return a || '—';
  }
  function rowClass(/** @type {any} */ o) {
    if (o.OrderStatus === 'resolved' && o.ResolvedPNLCents > 0) return 'row-win';
    if (o.OrderStatus === 'resolved' && o.ResolvedPNLCents < 0) return 'row-loss';
    if (o.OrderStatus === 'failed') return 'row-failed';
    return '';
  }
  // Potential profit if order fills and contract wins: (1 - price) * size * 100 cents.
  // Uses FillCount if filled, SuggestedSize if not. Sells show — (closing, not opening).
  function potentialProfit(/** @type {any} */ o) {
    if (o.Side === 'close' || o.Action === 'sell') return null;
    const size = o.FillCount > 0 ? o.FillCount : (o.SuggestedSize || 0);
    if (!size || !o.MarketPrice) return null;
    return Math.round((1 - o.MarketPrice) * size * 100);
  }
  function kalshiMarketURL(/** @type {any} */ o) {
    // URL format: https://kalshi.com/markets/{series_lower}/{event_ticker_lower}
    // Kalshi auto-redirects to the full slug URL. Series = first segment of
    // event ticker before the first dash (e.g. KXITFWMATCH-26JUL24IVAGHI -> KXITFWMATCH).
    const eventTicker = o.MatchTicker || '';
    const series = eventTicker.split('-')[0];
    return `https://kalshi.com/markets/${series.toLowerCase()}/${eventTicker.toLowerCase()}`;
  }

  // Stale indicator
  let staleSecs = $state(0);
  $effect(() => {
    const t = setInterval(() => {
      staleSecs = Math.floor((Date.now() - lastFetch) / 1000);
    }, 1000);
    return () => clearInterval(t);
  });

  // Keyboard: / to focus search
  /** @type {HTMLInputElement | null} */
  let searchInput = $state(null);
  function onKey(/** @type {KeyboardEvent} */ e) {
    if (e.key === '/' && document.activeElement?.tagName !== 'INPUT' && activeTab === 'orders') {
      e.preventDefault();
      searchInput?.focus();
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<svelte:head><title>Real Orders — Kalshi Ghost Trader</title></svelte:head>

<div class="page-container">
  <PageHeader title="Real Orders" {connected} error={error || ''} />

  <!-- Pool card -->
  {#if pool}
    <div class="pool-card">
      <div class="pool-main">
        <div class="pool-balance">
          <span class="pool-label">Balance</span>
          <span class="pool-value">{fmtCents(pool.balance_cents)}</span>
        </div>
        <div class="pool-secondary">
          <div class="pool-stat">
            <span class="pool-label">Initial</span>
            <span class="pool-sub">{fmtCents(pool.initial_balance_cents)}</span>
          </div>
          <div class="pool-stat">
            <span class="pool-label">Spent</span>
            <span class="pool-sub">{fmtCents(pool.total_spent_cents)}</span>
          </div>
          <div class="pool-stat">
            <span class="pool-label">Realized P&L</span>
            <span class="pool-sub" class:win={pool.total_pnl_cents > 0} class:loss={pool.total_pnl_cents < 0}>
              {fmtCents(pool.total_pnl_cents)}
            </span>
          </div>
          <div class="pool-stat">
            <span class="pool-label">Open Exposure</span>
            <span class="pool-sub">{fmtCents(exposureCents)}</span>
          </div>
        </div>
      </div>
      <button class="pool-manage" onclick={() => (poolModalOpen = true)}>Manage Pool</button>
    </div>
  {:else if !connected}
    <div class="pool-card skeleton-pool"><div class="skel-line"></div></div>
  {/if}

  <!-- Metrics bar: primary 6 + expandable secondary -->
  <MetricsBar
    primary={[
      { label: 'Net P&L', value: fmtCents(summary.pnl), tone: summary.pnl > 0 ? 'win' : summary.pnl < 0 ? 'loss' : null },
      { label: 'ROI', value: fmtPct(summary.roi), tone: summary.roi > 0 ? 'win' : summary.roi < 0 ? 'loss' : null },
      { label: 'Win Rate', value: summary.resolved > 0 ? summary.wr.toFixed(1) + '%' : '—' },
      { label: 'Sharpe', value: fmtRatio(summary.sh) },
      { label: 'Total', value: summary.total },
      { label: 'Days', value: summary.days },
    ]}
    secondary={[
      { label: 'Filled', value: summary.filled },
      { label: 'Resolved', value: summary.resolved },
      { label: 'Failed', value: summary.failed, tone: 'loss' },
      { label: 'Daily Avg', value: fmtSignedCents(summary.dAvg), tone: summary.dAvg > 0 ? 'win' : summary.dAvg < 0 ? 'loss' : null },
      { label: 'Avg/Trade', value: fmtSignedCents(summary.avgPnl), tone: summary.avgPnl > 0 ? 'win' : summary.avgPnl < 0 ? 'loss' : null },
      { label: 'Sortino', value: fmtRatio(summary.so) },
      { label: 'Profit Factor', value: fmtRatio(summary.pf) },
      { label: 'Max DD', value: fmtCents(summary.mdd), tone: 'loss' },
      { label: 'P&L Std', value: fmtCents(summary.pnlStd) },
    ]}
    note={staleSecs > 8 ? `last update ${staleSecs}s ago` : ''}
  />

  <!-- Risk dashboard -->
  <RiskDashboard liquidityPool={pool} pendingOrders={openOrders} settledOrders={orders.filter((o) => o.Result === 'yes' || o.Result === 'no')} />

  <!-- Tabs -->
  <Tabs
    tabs={[
      { key: 'orders', label: 'Orders', count: orders.length },
      { key: 'strategy', label: 'By Strategy', count: Object.keys(strategyMetrics).length },
      { key: 'open', label: 'Open Positions', count: openOrders.length },
      { key: 'pool', label: 'Pool History' },
    ]}
    bind:active={activeTab}
  />

  <!-- Orders tab -->
  {#if activeTab === 'orders'}
    <div class="toolbar">
      <input
        class="search"
        type="text"
        placeholder="Search match, player, strategy, order id  (/)"
        bind:this={searchInput}
        bind:value={search}
        oninput={() => (page = 0)}
      />
      <div class="chips">
        <button class="chip-clear" onclick={clearStatus} disabled={statusFilter.size === 0}>Clear</button>
        {#each ALL_STATUSES as s}
          {@const count = statusCounts[s] || 0}
          {#if count > 0}
            <button
              class="chip"
              class:active={statusFilter.has(s)}
              class:chip-err={s === 'failed'}
              class:chip-ok={s === 'filled' || s === 'resolved'}
              class:chip-loading={s === 'submitted' || s === 'partial'}
              onclick={() => toggleStatus(s)}
            >
              {s} <span class="chip-count">{count}</span>
            </button>
          {/if}
        {/each}
      </div>
      <select bind:value={strategyFilter} onchange={() => (page = 0)} class="strategy-select">
        <option value="">All Strategies</option>
        {#each strategies as s}
          <option value={s}>{s}</option>
        {/each}
      </select>
      <span class="result-count">{filteredOrders.length} orders</span>
      <button class="export-btn" onclick={() => {
        const headers = ['Time', 'Match', 'Player', 'Strategy', 'Side', 'Size', 'Fill', 'Price', 'Edge', 'Status', 'P&L', 'Order ID'];
        const rows = filteredOrders.map((o) => [
          new Date(o.TS).toISOString(),
          o.MatchTitle || o.MatchTicker,
          o.PlayerName || '',
          o.Strategy,
          o.Action,
          o.SuggestedSize,
          o.FillCount,
          o.MarketPrice,
          o.EdgeCents,
          o.OrderStatus,
          o.ResolvedPNLCents / 100,
          o.KalshiOrderID || '',
        ]);
        exportCSV(headers, rows, `real_orders_${Date.now()}.csv`);
      }}>Export CSV</button>
    </div>

    {#if !connected && orders.length === 0}
      <Skeleton rows={8} cols={8} />
    {:else if filteredOrders.length === 0}
      <EmptyState text={orders.length === 0 ? 'No real orders yet' : 'No orders match current filters'} />
    {:else}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th class="sortable" onclick={() => toggleSort('TS')}>Time {#if sortKey === 'TS'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th class="sortable" onclick={() => toggleSort('MatchTitle')}>Match {#if sortKey === 'MatchTitle'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th>Player</th>
              <th class="sortable" onclick={() => toggleSort('Strategy')}>Strategy {#if sortKey === 'Strategy'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th>Side</th>
              <th class="num sortable" onclick={() => toggleSort('SuggestedSize')}>Size@Price {#if sortKey === 'SuggestedSize'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th class="num">Fill</th>
              <th class="sortable" onclick={() => toggleSort('OrderStatus')}>Status {#if sortKey === 'OrderStatus'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th class="num sortable" onclick={() => toggleSort('ResolvedPNLCents')}>P&L {#if sortKey === 'ResolvedPNLCents'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th class="num sortable" onclick={() => toggleSort('_roi')}>ROI {#if sortKey === '_roi'}{sortDir === 'asc' ? '▲' : '▼'}{/if}</th>
              <th class="num">Potential</th>
              <th>Kalshi</th>
            </tr>
          </thead>
          <tbody>
            {#each pageOrders as o (o.ID)}
              {@const oROI = orderROI(o)}
              <tr class="clickable {rowClass(o)}" onclick={() => openDrawer(o)}>
                <td class="mono">{fmtTimeShort(o.TS)}</td>
                <td class="match-cell">{o.MatchTitle || fmtTicker(o.MatchTicker)}</td>
                <td>{o.PlayerName || o.MarketTicker}</td>
                <td>{o.Strategy}</td>
                <td><span class="side-tag side-{o.Action}">{sideLabel(o.Action)}</span></td>
                <td class="num">{o.SuggestedSize?.toFixed(2)} @ {(o.MarketPrice ?? 0).toFixed(3)}</td>
                <td class="num">{o.FillCount ? o.FillCount.toFixed(2) : '—'}</td>
                <td><span class="badge badge-{statusVariant(o.OrderStatus)}">{o.OrderStatus || 'pending'}</span></td>
                <td class="num">
                  {#if o.ResolvedPNLCents}
                    <span class:win={o.ResolvedPNLCents > 0} class:loss={o.ResolvedPNLCents < 0}>
                      {fmtCents(o.ResolvedPNLCents)}
                    </span>
                  {:else}
                    <span class="muted">—</span>
                  {/if}
                </td>
                <td class="num">
                  {#if oROI !== null}
                    <span class:win={oROI > 0} class:loss={oROI < 0}>{fmtPct(oROI)}</span>
                  {:else}
                    <span class="muted">—</span>
                  {/if}
                </td>
                <td class="num">
                  {#if potentialProfit(o) !== null}
                    <span>+{fmtCents(/** @type {number} */ (potentialProfit(o)))}</span>
                  {:else}
                    <span class="muted">—</span>
                  {/if}
                </td>
                <td>
                  <a href={kalshiMarketURL(o)} target="_blank" rel="noopener noreferrer" class="kalshi-link" onclick={(e) => e.stopPropagation()}>Open ↗</a>
                </td>
              </tr>
            {/each}
          </tbody>
          <tfoot>
            <tr class="table-footer">
              <td colspan="6"><strong>{filteredOrders.length} orders</strong> ({summary.resolved} resolved, {summary.wins}W / {summary.losses}L)</td>
              <td class="num"></td>
              <td></td>
              <td class="num"><strong class:win={summary.pnl > 0} class:loss={summary.pnl < 0}>{fmtCents(summary.pnl)}</strong></td>
              <td class="num"><strong class:win={summary.roi > 0} class:loss={summary.roi < 0}>{fmtPct(summary.roi)}</strong></td>
              <td></td>
              <td></td>
            </tr>
          </tfoot>
        </table>
      </div>

      {#if totalPages > 1}
        <div class="pager">
          <button disabled={page === 0} onclick={() => (page = 0)}>«</button>
          <button disabled={page === 0} onclick={() => (page -= 1)}>Prev</button>
          <span class="pager-info">Page {page + 1} / {totalPages}</span>
          <button disabled={page >= totalPages - 1} onclick={() => (page += 1)}>Next</button>
          <button disabled={page >= totalPages - 1} onclick={() => (page = totalPages - 1)}>»</button>
        </div>
      {/if}
    {/if}
  {/if}

  <!-- By Strategy tab -->
  {#if activeTab === 'strategy'}
    {#if orders.length === 0}
      <EmptyState text="No strategy metrics yet" />
    {:else}
      {@const stratEntries = Object.entries(strategyMetrics).sort((a, b) => b[1].netPnl - a[1].netPnl)}
      <div class="strategy-grid">
        {#each stratEntries as [name, m] (name)}
          {@const positive = m.netPnl > 0}
          <div class="strategy-card">
            <div class="sc-header">
              <span class="sc-name">{name}</span>
              <span class="sc-pnl" class:win={positive} class:loss={!positive}>{fmtCents(m.netPnl)}</span>
            </div>
            <div class="sc-grid">
              <div class="sc-stat"><span>Orders</span><b>{m.total}</b></div>
              <div class="sc-stat"><span>Resolved</span><b>{m.resolved}</b></div>
              <div class="sc-stat"><span>Wins</span><b class="win">{m.wins}</b></div>
              <div class="sc-stat"><span>Losses</span><b class="loss">{m.losses}</b></div>
              <div class="sc-stat"><span>Win Rate</span><b>{m.resolved > 0 ? m.winRate.toFixed(1) + '%' : '—'}</b></div>
              <div class="sc-stat"><span>ROI</span><b class:win={m.roi > 0} class:loss={m.roi < 0}>{fmtPct(m.roi)}</b></div>
              <div class="sc-stat"><span>Invested</span><b>${(m.invested / 100).toFixed(2)}</b></div>
              <div class="sc-stat"><span>Net P&L</span><b class:win={m.netPnl > 0} class:loss={m.netPnl < 0}>{fmtCents(m.netPnl)}</b></div>
              <div class="sc-stat"><span>Daily Avg</span><b class:win={m.dailyAvg > 0} class:loss={m.dailyAvg < 0}>{fmtSignedCents(m.dailyAvg)}</b></div>
              <div class="sc-stat"><span>Avg/Trade</span><b class:win={m.avgPnl > 0} class:loss={m.avgPnl < 0}>{fmtSignedCents(m.avgPnl)}</b></div>
              <div class="sc-stat"><span>Sharpe</span><b>{fmtRatio(m.sharpe)}</b></div>
              <div class="sc-stat"><span>Sortino</span><b>{fmtRatio(m.sortino)}</b></div>
              <div class="sc-stat"><span>Profit Factor</span><b>{fmtRatio(m.profitFactor)}</b></div>
              <div class="sc-stat"><span>Max DD</span><b class="loss">{fmtCents(m.maxDD)}</b></div>
              <div class="sc-stat"><span>P&L Std</span><b>{fmtCents(m.pnlStd)}</b></div>
              <div class="sc-stat"><span>Days</span><b>{m.days}</b></div>
            </div>
          </div>
        {/each}
        <div class="strategy-card total-card">
          <div class="sc-header">
            <span class="sc-name">Total</span>
            <span class="sc-pnl" class:win={summary.pnl > 0} class:loss={summary.pnl < 0}>{fmtCents(summary.pnl)}</span>
          </div>
          <div class="sc-grid">
            <div class="sc-stat"><span>Orders</span><b>{summary.total}</b></div>
            <div class="sc-stat"><span>Resolved</span><b>{summary.resolved}</b></div>
            <div class="sc-stat"><span>Wins</span><b class="win">{summary.wins}</b></div>
            <div class="sc-stat"><span>Losses</span><b class="loss">{summary.losses}</b></div>
            <div class="sc-stat"><span>Win Rate</span><b>{summary.resolved > 0 ? summary.wr.toFixed(1) + '%' : '—'}</b></div>
            <div class="sc-stat"><span>ROI</span><b class:win={summary.roi > 0} class:loss={summary.roi < 0}>{fmtPct(summary.roi)}</b></div>
            <div class="sc-stat"><span>Invested</span><b>${(summary.investedCents / 100).toFixed(2)}</b></div>
            <div class="sc-stat"><span>Net P&L</span><b class:win={summary.pnl > 0} class:loss={summary.pnl < 0}>{fmtCents(summary.pnl)}</b></div>
            <div class="sc-stat"><span>Daily Avg</span><b class:win={summary.dAvg > 0} class:loss={summary.dAvg < 0}>{fmtSignedCents(summary.dAvg)}</b></div>
            <div class="sc-stat"><span>Avg/Trade</span><b class:win={summary.avgPnl > 0} class:loss={summary.avgPnl < 0}>{fmtSignedCents(summary.avgPnl)}</b></div>
            <div class="sc-stat"><span>Sharpe</span><b>{fmtRatio(summary.sh)}</b></div>
            <div class="sc-stat"><span>Sortino</span><b>{fmtRatio(summary.so)}</b></div>
            <div class="sc-stat"><span>Profit Factor</span><b>{fmtRatio(summary.pf)}</b></div>
            <div class="sc-stat"><span>Max DD</span><b class="loss">{fmtCents(summary.mdd)}</b></div>
            <div class="sc-stat"><span>P&L Std</span><b>{fmtCents(summary.pnlStd)}</b></div>
            <div class="sc-stat"><span>Days</span><b>{summary.days}</b></div>
          </div>
        </div>
      </div>
    {/if}
  {/if}

  <!-- Open Positions tab -->
  {#if activeTab === 'open'}
    {#if openOrders.length === 0}
      <EmptyState text="No open positions" />
    {:else}
      {@const avgEntry = openOrders.length > 0
        ? openOrders.reduce((s, o) => s + (o.MarketPrice || 0), 0) / openOrders.length
        : 0}
      {@const totalFill = openOrders.reduce((s, o) => s + (o.FillCount || 0), 0)}
      {@const byStrategy = openOrders.reduce((acc, o) => {
        const k = o.Strategy || 'unknown';
        acc[k] = (acc[k] || 0) + 1;
        return acc;
      }, {})}
      <div class="exposure-bar">
        <span class="exposure-label">Total Open Exposure</span>
        <span class="exposure-value">{fmtCents(exposureCents)}</span>
        <span class="exposure-stat"><span class="exposure-label">Avg Entry</span><b>{(avgEntry * 100).toFixed(1)}¢</b></span>
        <span class="exposure-stat"><span class="exposure-label">Total Fill</span><b>{totalFill.toFixed(2)}</b></span>
        <span class="exposure-stat"><span class="exposure-label">Strategies</span><b>{Object.keys(byStrategy).length}</b></span>
        <span class="exposure-count">{openOrders.length} position{openOrders.length === 1 ? '' : 's'}</span>
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Time</th>
              <th>Match</th>
              <th>Player</th>
              <th>Strategy</th>
              <th>Side</th>
              <th class="num">Size</th>
              <th class="num">Fill</th>
              <th class="num">Price</th>
              <th class="num">Cost</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {#each openOrders as o}
              {@const cost = (o.FillCount || 0) * (o.MarketPrice || 0)}
              <tr>
                <td class="mono">{fmtDate(o.TS)}</td>
                <td class="mono">{fmtTicker(o.MatchTicker)}</td>
                <td>{o.PlayerName || o.MarketTicker}</td>
                <td>{o.Strategy}</td>
                <td><span class="side-tag side-{o.Action}">{sideLabel(o.Action)}</span></td>
                <td class="num">{o.SuggestedSize?.toFixed(2)}</td>
                <td class="num">{o.FillCount ? o.FillCount.toFixed(2) : '—'}</td>
                <td class="num">{(o.MarketPrice ?? 0).toFixed(3)}</td>
                <td class="num">{fmtCents(cost)}</td>
                <td><span class="badge badge-{statusVariant(o.OrderStatus)}">{o.OrderStatus}</span></td>
              </tr>
            {/each}
          </tbody>
          <tfoot>
            <tr class="table-footer">
              <td colspan="5"><strong>{openOrders.length} open positions</strong></td>
              <td class="num"></td>
              <td class="num"><strong>{totalFill.toFixed(2)}</strong></td>
              <td class="num"><strong>{(avgEntry * 100).toFixed(1)}¢</strong></td>
              <td class="num"><strong>{fmtCents(exposureCents)}</strong></td>
              <td></td>
            </tr>
          </tfoot>
        </table>
      </div>
    {/if}
  {/if}

  {#if activeTab === 'pool'}
    <PoolHistory />
  {/if}
</div>

<!-- Order detail drawer -->
<Drawer open={drawerOpen} title="Order Detail" onClose={closeDrawer}>
  {#if selectedOrder}
    {@const o = selectedOrder}
    <div class="drawer-content">
      <div class="d-section">
        <div class="d-row"><span class="d-key">Status</span><span class="badge badge-{statusVariant(o.OrderStatus)}">{o.OrderStatus || 'pending'}</span></div>
        <div class="d-row"><span class="d-key">P&L</span><span class:win={o.ResolvedPNLCents > 0} class:loss={o.ResolvedPNLCents < 0}>{fmtCents(o.ResolvedPNLCents)}</span></div>
        <div class="d-row"><span class="d-key">Strategy</span><span>{o.Strategy}</span></div>
        <div class="d-row"><span class="d-key">Side</span><span class="side-tag side-{o.Action}">{sideLabel(o.Action)}</span></div>
      </div>

      <div class="d-section">
        <h3>Match</h3>
        <div class="d-row"><span class="d-key">Match</span><span>{o.MatchTitle || fmtTicker(o.MatchTicker)}</span></div>
        <div class="d-row"><span class="d-key">Player</span><span>{o.PlayerName || o.MarketTicker}</span></div>
        <div class="d-row"><span class="d-key">Series</span><span class="series">{seriesFromTicker(o.MatchTicker)}</span></div>
        <a class="d-link" href={`/matches/${encodeURIComponent(o.MatchTicker)}`}>View match →</a>
      </div>

      <div class="d-section">
        <h3>Execution</h3>
        <div class="d-row"><span class="d-key">Size</span><span class="mono">{o.SuggestedSize?.toFixed(4)}</span></div>
        <div class="d-row"><span class="d-key">Fill</span><span class="mono">{o.FillCount ? o.FillCount.toFixed(4) : '—'}</span></div>
        <div class="d-row"><span class="d-key">Price</span><span class="mono">{(o.MarketPrice ?? 0).toFixed(4)}</span></div>
        <div class="d-row"><span class="d-key">Edge</span><span class="mono">{o.EdgeCents ?? 0}¢</span></div>
        <div class="d-row"><span class="d-key">Conv Prob</span><span class="mono">{o.ConvProb ? (o.ConvProb * 100).toFixed(1) + '%' : '—'}</span></div>
        <div class="d-row"><span class="d-key">Set</span><span class="mono">{o.SetNumber ?? '—'}</span></div>
      </div>

      <div class="d-section">
        <h3>Sizing</h3>
        <div class="d-row"><span class="d-key">Bankroll</span><span class="mono">${o.Bankroll?.toFixed(2)}</span></div>
        <div class="d-row"><span class="d-key">Kelly Frac</span><span class="mono">{o.KellyFraction ? (o.KellyFraction * 100).toFixed(2) + '%' : '—'}</span></div>
      </div>

      <div class="d-section">
        <h3>Pool Impact</h3>
        <div class="d-row"><span class="d-key">Before</span><span class="mono">{fmtCents(o.PoolBalanceBeforeCents)}</span></div>
        <div class="d-row"><span class="d-key">After</span><span class="mono">{fmtCents(o.PoolBalanceAfterCents)}</span></div>
        <div class="d-row"><span class="d-key">Unfilled Refund</span><span class="mono">{fmtCents(o.UnfilledRefundedCents)}</span></div>
        {#if potentialProfit(o) !== null}<div class="d-row"><span class="d-key">Potential Profit</span><span class="mono">+{fmtCents(/** @type {number} */ (potentialProfit(o)))}</span></div>{/if}
      </div>

      <div class="d-section">
        <h3>Reference</h3>
        <div class="d-row"><span class="d-key">Order ID</span><span class="mono">{o.KalshiOrderID || '—'}</span></div>
        <div class="d-row"><span class="d-key">Internal ID</span><span class="mono">{o.ID}</span></div>
        <div class="d-row"><span class="d-key">Market</span><span class="mono">{o.MarketTicker}</span></div>
        <div class="d-row"><span class="d-key">Kalshi</span><a href={kalshiMarketURL(o)} target="_blank" rel="noopener noreferrer" class="kalshi-link">Open market ↗</a></div>
        <div class="d-row"><span class="d-key">Pair ID</span><span class="mono">{o.PairID || '—'}</span></div>
        <div class="d-row"><span class="d-key">Time</span><span class="mono">{fmtDate(o.TS)}</span></div>
        {#if o.SettledTS}<div class="d-row"><span class="d-key">Settled</span><span class="mono">{fmtDate(o.SettledTS)}</span></div>{/if}
        {#if o.Result}<div class="d-row"><span class="d-key">Result</span><span>{o.Result}</span></div>{/if}
      </div>

      {#if o.Context}
        <div class="d-section">
          <h3>Context</h3>
          <div class="d-context">{o.Context}</div>
        </div>
      {/if}
    </div>
  {/if}
</Drawer>

<!-- Pool manage modal -->
<Modal open={poolModalOpen} title="Manage Liquidity Pool" onClose={() => (poolModalOpen = false)}>
  <div class="pool-modal">
    {#if pool}
      <div class="pm-current">
        <div class="pm-stat"><span>Balance</span><b>{fmtCents(pool.balance_cents)}</b></div>
        <div class="pm-stat"><span>Initial</span><b>{fmtCents(pool.initial_balance_cents)}</b></div>
        <div class="pm-stat"><span>Spent</span><b>{fmtCents(pool.total_spent_cents)}</b></div>
        <div class="pm-stat"><span>Realized P&L</span><b class:win={pool.total_pnl_cents > 0} class:loss={pool.total_pnl_cents < 0}>{fmtCents(pool.total_pnl_cents)}</b></div>
      </div>
    {/if}

    <div class="pm-section">
      <h3>Top Up</h3>
      <p class="pm-hint">Add capital. Preserves history.</p>
      <div class="pm-input-row">
        <input type="number" bind:value={topupDollars} placeholder="5.00" step="0.01" min="0" />
        <button class="pm-btn" onclick={handleTopUp} disabled={poolBusy}>Add</button>
      </div>
    </div>

    <div class="pm-section pm-danger">
      <h3>Reset Pool</h3>
      <p class="pm-hint">Wipes total_spent + total_pnl. Sets new balance. Destructive.</p>
      <div class="pm-input-row">
        <input type="number" bind:value={resetDollars} placeholder="20.00" step="0.01" min="0" />
      </div>
      <p class="pm-confirm-hint">Type the amount to confirm: <code>{resetDollars || '—'}</code></p>
      <div class="pm-input-row">
        <input type="text" bind:value={resetConfirm} placeholder="re-type amount" autocomplete="off" />
        <button class="pm-btn pm-btn-warn" onclick={handleReset} disabled={poolBusy}>Reset</button>
      </div>
    </div>
  </div>
</Modal>

<Toaster />

<style>
  .win { color: var(--win); }
  .loss { color: var(--loss); }
  .muted { color: var(--text-dim); }
  .mono { font-family: var(--font-mono); font-size: 12px; color: #94a3b8; }
  .series { color: var(--accent); font-size: 12px; }

  /* Pool card */
  .pool-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 18px 20px;
    margin-bottom: 16px;
  }
  .pool-main { display: flex; align-items: center; gap: 32px; flex-wrap: wrap; flex: 1; }
  .pool-balance { display: flex; flex-direction: column; gap: 2px; }
  .pool-label { font-size: 10px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; }
  .pool-value { font-size: 28px; font-weight: 800; color: var(--text-bright); }
  .pool-secondary { display: flex; gap: 28px; flex-wrap: wrap; }
  .pool-stat { display: flex; flex-direction: column; gap: 2px; }
  .pool-sub { font-size: 16px; font-weight: 700; color: var(--text); }
  .pool-manage {
    padding: 8px 18px;
    background: var(--surface-hover);
    color: var(--text);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    cursor: pointer;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    white-space: nowrap;
  }
  .pool-manage:hover { background: var(--border-strong); }
  .skeleton-pool { height: 64px; }
  .skel-line {
    height: 100%;
    background: linear-gradient(90deg, var(--surface-hover) 25%, var(--border-strong) 50%, var(--surface-hover) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: var(--radius);
  }
  @keyframes shimmer { from { background-position: 200% 0; } to { background-position: -200% 0; } }

  /* Table footer */
  .table-footer {
    background: var(--surface-hover);
    border-top: 2px solid var(--border-strong);
  }
  .table-footer td {
    font-size: 13px;
    padding: 10px 14px;
  }

  /* Exposure bar extras */
  .exposure-stat { display: flex; flex-direction: column; gap: 2px; }
  .exposure-stat b { font-size: 16px; font-weight: 700; color: var(--text-bright); }

  /* Toolbar */
  .toolbar {
    display: flex;
    gap: 12px;
    align-items: center;
    flex-wrap: wrap;
    margin-bottom: 14px;
  }
  .search {
    flex: 1;
    min-width: 240px;
    padding: 8px 12px;
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: 13px;
  }
  .search:focus { outline: none; border-color: var(--accent); }
  .chips { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .chip-clear {
    padding: 4px 10px;
    background: none;
    border: 1px solid var(--border);
    color: var(--text-muted);
    border-radius: 12px;
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }
  .chip-clear:disabled { opacity: 0.4; cursor: default; }
  .chip {
    padding: 4px 10px;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--text-muted);
    border-radius: 12px;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 5px;
    transition: all 0.12s;
  }
  .chip:hover { border-color: var(--border-strong); color: var(--text); }
  .chip.active { border-color: var(--accent); color: var(--accent); background: var(--loading-bg); }
  .chip.active.chip-err { border-color: var(--loss); color: var(--loss); background: var(--loss-bg); }
  .chip.active.chip-ok { border-color: var(--win); color: var(--win); background: var(--win-bg); }
  .chip.active.chip-loading { border-color: var(--loading); color: var(--loading); background: var(--loading-bg); }
  .chip-count { font-size: 10px; opacity: 0.7; }
  .strategy-select {
    padding: 7px 10px;
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: 13px;
  }
  .result-count { font-size: 12px; color: var(--text-muted); margin-left: auto; }
  .export-btn {
    background: var(--surface-hover);
    border: 1px solid var(--border-strong);
    color: var(--text-muted);
    padding: 4px 12px;
    border-radius: var(--radius-xs);
    font-size: 12px;
    cursor: pointer;
  }
  .export-btn:hover { color: var(--text); border-color: var(--accent); }

  /* Table extras */
  .sortable { cursor: pointer; user-select: none; }
  .sortable:hover { color: var(--text); }
  .match-cell { max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .row-failed { border-left: 3px solid var(--loss); }
  .side-tag {
    font-size: 11px;
    font-weight: 700;
    padding: 2px 7px;
    border-radius: var(--radius-xs);
    background: var(--pending-bg);
    color: var(--text-muted);
  }
  .side-buy { background: var(--win-bg); color: var(--win); }
  .side-buy_no { background: var(--loss-bg); color: var(--loss); }
  .side-sell { background: var(--loading-bg); color: var(--loading); }

  /* Pager */
  .pager {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 14px;
  }
  .pager button {
    padding: 6px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--text);
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: 13px;
    cursor: pointer;
  }
  .pager button:hover:not(:disabled) { background: var(--surface-hover); }
  .pager button:disabled { opacity: 0.4; cursor: default; }
  .pager-info { font-size: 13px; color: var(--text-muted); }

  /* Strategy grid */
  .strategy-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 12px;
  }
  .strategy-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 14px 16px;
  }
  .total-card { border-color: var(--border-strong); background: var(--surface-hover); }
  .sc-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 12px;
  }
  .sc-name { font-size: 15px; font-weight: 700; color: var(--text-bright); }
  .sc-pnl { font-size: 18px; font-weight: 800; }
  .sc-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px 14px;
  }
  .sc-stat { display: flex; justify-content: space-between; font-size: 12px; }
  .sc-stat span { color: var(--text-muted); }
  .sc-stat b { color: var(--text); font-weight: 600; }

  /* Open positions */
  .exposure-bar {
    display: flex;
    align-items: center;
    gap: 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 14px 20px;
    margin-bottom: 14px;
  }
  .exposure-label { font-size: 11px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; }
  .exposure-value { font-size: 22px; font-weight: 800; color: var(--text-bright); }
  .exposure-count { font-size: 13px; color: var(--text-muted); margin-left: auto; }

  /* Drawer content */
  .drawer-content { display: flex; flex-direction: column; gap: 20px; }
  .d-section { display: flex; flex-direction: column; gap: 8px; }
  .d-section h3 {
    font-size: 11px;
    font-weight: 700;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin: 0;
    padding-bottom: 6px;
    border-bottom: 1px solid var(--border);
  }
  .d-row { display: flex; justify-content: space-between; align-items: center; font-size: 13px; }
  .d-key { color: var(--text-muted); }
  .d-link { color: var(--accent); text-decoration: none; font-size: 13px; font-weight: 600; }
  .d-link:hover { text-decoration: underline; }
  .kalshi-link { color: var(--accent); text-decoration: none; font-size: 12px; font-weight: 600; white-space: nowrap; }
  .kalshi-link:hover { text-decoration: underline; }
  .d-context {
    font-size: 12px;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 10px 12px;
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.6;
  }

  /* Pool modal */
  .pool-modal { display: flex; flex-direction: column; gap: 20px; }
  .pm-current {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
    padding: 14px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }
  .pm-stat { display: flex; flex-direction: column; gap: 2px; }
  .pm-stat span { font-size: 10px; color: var(--text-muted); text-transform: uppercase; }
  .pm-stat b { font-size: 16px; color: var(--text-bright); }
  .pm-section { display: flex; flex-direction: column; gap: 8px; }
  .pm-section h3 { font-size: 14px; font-weight: 700; color: var(--text-bright); margin: 0; }
  .pm-hint { font-size: 12px; color: var(--text-muted); margin: 0; }
  .pm-input-row { display: flex; gap: 8px; }
  .pm-input-row input {
    flex: 1;
    padding: 8px 12px;
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: 14px;
  }
  .pm-input-row input:focus { outline: none; border-color: var(--accent); }
  .pm-btn {
    padding: 8px 18px;
    background: var(--accent);
    color: white;
    border: none;
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
  }
  .pm-btn:hover { filter: brightness(1.1); }
  .pm-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .pm-btn-warn { background: var(--loss); }
  .pm-danger { padding-top: 16px; border-top: 1px solid var(--border); }
  .pm-confirm-hint { font-size: 12px; color: var(--text-muted); margin: 0; }
  .pm-confirm-hint code { font-family: var(--font-mono); color: var(--warn); }
</style>
