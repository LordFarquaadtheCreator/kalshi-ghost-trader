<script>
  import { api } from '$lib/api.js';
  import { fmtTime, fmtTicker, seriesFromTicker, fmtPnL, fmtPct, vibrantColor } from '$lib/utils.js';
  import {
    dailySeries, maxDrawdown, sharpeDaily, sortinoDaily, dailyAvgPnL,
    profitFactor, mean, stdDev,
  } from '$lib/stats.js';
  import { browser } from '$app/environment';
  import { goto } from '$app/navigation';
  import { exportCSV } from '$lib/csv.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import PaperOrdersInsights from '$lib/components/PaperOrdersInsights.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';
  import Tabs from '$lib/components/Tabs.svelte';

  let { data } = $props();

  const PAGE_SIZE = 100;

  // --- Filter state (drives server-side queries) ---
  let selectedStrategies = $state(new Set());
  let minPrice = $state(0);
  let maxPrice = $state(0);
  let filterMatch = $state('');
  let filterResult = $state('');

  // --- Data state ---
  /** @type {any} */
  let summary = $state(data?.summary ?? null);
  /** @type {any[]} */
  let orders = $state(data?.page?.orders ?? []);
  let hasMore = $state(data?.page?.has_more ?? false);
  /** @type {{ts: number, id: number} | null} */
  let nextCursor = $state(data?.page?.next_cursor ?? null);
  let strategies = $state(data?.meta?.strategies ?? []);
  let loadingMore = $state(false);
  let loading = $state(!data?.page);
  /** @type {string | null} */
  let error = $state(null);
  let connected = $state(!!data?.page);

  let strategiesInitialized = false;
  $effect(() => {
    if (!strategiesInitialized && strategies.length > 0) {
      strategiesInitialized = true;
      selectedStrategies = new Set(strategies);
    }
  });

  /** @type {Record<string, string>} */
  const strategyColors = {
    'matchpoint': '#60a5fa',
    'matchpoint-aggro': '#a78bfa',
    'setpoint': '#34d399',
    'setpoint-serve': '#fbbf24',
    'setpoint-cheap': '#f472b0',
    'fadelongshot': '#f87171',
  };

  function colorFor(/** @type {string} */ name) {
    return strategyColors[name] || vibrantColor(name);
  }

  // Build filter object from current state.
  function currentFilters() {
    /** @type {{strategies?: string[], min_price?: number, max_price?: number, match?: string, result?: string}} */
    const f = {};
    if (selectedStrategies.size > 0 && selectedStrategies.size < strategies.length) {
      f.strategies = [...selectedStrategies];
    }
    if (minPrice > 0) f.min_price = minPrice;
    if (maxPrice > 0) f.max_price = maxPrice;
    if (filterMatch) f.match = filterMatch;
    if (filterResult) f.result = filterResult;
    return f;
  }

  // Map frontend result filter to API result param.
  // "won"/"lost" → "yes"/"no" (result on order row).
  function apiResultFilter() {
    if (filterResult === 'won') return 'yes';
    if (filterResult === 'lost') return 'no';
    if (filterResult === 'pending') return 'pending';
    return '';
  }

  function currentFiltersWithResult() {
    const f = currentFilters();
    const r = apiResultFilter();
    if (r) f.result = r;
    else delete f.result;
    return f;
  }

  // --- Refetch everything when filters change ---
  let filtersChanged = $state(0);
  $effect(() => {
    // Track filter state changes
    selectedStrategies.size;
    minPrice;
    maxPrice;
    filterMatch;
    filterResult;
    if (!browser || !strategiesInitialized) return;
    filtersChanged++;
  });

  $effect(() => {
    if (filtersChanged === 0) return;
    refetchAll();
  });

  async function refetchAll() {
    loading = true;
    error = null;
    try {
      const filters = currentFiltersWithResult();
      const [s, p] = await Promise.all([
        api.getPaperOrdersSummary(filters),
        api.getPaperOrdersPage({ ...filters, limit: PAGE_SIZE }),
      ]);
      summary = s;
      orders = p.orders ?? [];
      hasMore = p.has_more ?? false;
      nextCursor = p.next_cursor ?? null;
      connected = true;
    } catch (e) {
      error = String(e);
      connected = false;
    } finally {
      loading = false;
    }
  }

  // --- Load more (cursor pagination) ---
  async function loadMore() {
    if (!hasMore || loadingMore || !nextCursor) return;
    loadingMore = true;
    try {
      const filters = currentFiltersWithResult();
      const p = await api.getPaperOrdersPage({
        ...filters,
        cursor_ts: nextCursor.ts,
        cursor_id: nextCursor.id,
        limit: PAGE_SIZE,
      });
      orders = [...orders, ...(p.orders ?? [])];
      hasMore = p.has_more ?? false;
      nextCursor = p.next_cursor ?? null;
    } catch (e) {
      error = String(e);
    } finally {
      loadingMore = false;
    }
  }

  // --- Delta polling: prepend new orders ---
  /** @type {ReturnType<typeof setInterval> | null} */
  let deltaTimer = $state(null);
  let lastTS = $derived(orders.length > 0 ? orders[0].ts : 0);

  $effect(() => {
    if (!browser || !strategiesInitialized) return;
    if (deltaTimer) clearInterval(deltaTimer);
    deltaTimer = setInterval(async () => {
      if (!browser || lastTS === 0) return;
      try {
        const filters = currentFiltersWithResult();
        const delta = await api.getPaperOrdersDelta(lastTS, filters);
        if (delta && delta.orders && delta.orders.length > 0) {
          // Prepend new orders, cap at 500 total to avoid unbounded growth.
          orders = [...delta.orders, ...orders].slice(0, 500);
          // Refresh summary too — new orders change aggregates.
          summary = await api.getPaperOrdersSummary(filters);
        }
      } catch {}
    }, 5000);
    return () => { if (deltaTimer) clearInterval(deltaTimer); };
  });

  // --- Derived: split orders into pending/settled ---
  let pendingOrders = $derived(orders.filter((/** @type {any} */ o) => !o.result));
  let settledOrders = $derived(orders.filter((/** @type {any} */ o) => o.result));

  // --- Summary from server (total row) ---
  let totalSummary = $derived(summary?.total ?? null);

  // --- Strategy toggles ---
  function toggleStrategy(/** @type {string} */ name) {
    const next = new Set(selectedStrategies);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selectedStrategies = next;
  }

  function toggleAllStrategies() {
    if (selectedStrategies.size === strategies.length) selectedStrategies = new Set();
    else selectedStrategies = new Set(strategies);
  }

  // --- Insights component ref + refresh ---
  /** @type {any} */
  let insightsComp = $state(null);
  let insightsLoading = $state(false);
  let insightsRunTS = $derived(data?.insights?.insight_run_ts ?? 0);

  async function refreshInsights() {
    if (!insightsComp || insightsLoading) return;
    insightsLoading = true;
    try {
      await insightsComp.refresh(() => api.getPaperOrdersInsights());
    } finally {
      insightsLoading = false;
    }
  }

  /** @type {ReturnType<typeof setInterval> | null} */
  let insightsTimer = null;
  $effect(() => {
    if (!browser) return;
    insightsTimer = setInterval(() => { if (browser) refreshInsights(); }, 300_000);
    return () => { if (insightsTimer) clearInterval(insightsTimer); };
  });

  // Compute PnL per settled order (server returns result but not pnl).
  function orderPnL(/** @type {any} */ o) {
    if (!o.result) return 0;
    if (o.result === 'yes') return o.suggested_size * (1.0 - o.market_price);
    return -o.suggested_size * o.market_price;
  }
  function orderWon(/** @type {any} */ o) {
    return o.result === 'yes';
  }
  // Per-order ROI (dollars).
  function orderROI(/** @type {any} */ o) {
    const inv = o.suggested_size * o.market_price;
    if (!inv) return null;
    return (orderPnL(o) / inv) * 100;
  }
  // Per-order invested (dollars).
  function orderInvested(/** @type {any} */ o) {
    return o.suggested_size * o.market_price;
  }

  // --- Risk metrics (client-side, from loaded settled orders) ---
  let settledPnls = $derived(settledOrders.map((o) => orderPnL(o)));
  let dailySeriesData = $derived(dailySeries(settledOrders, (o) => o.ts, (o) => orderPnL(o)));

  let riskMetrics = $derived.by(() => {
    if (settledOrders.length === 0) return null;
    const pnls = settledPnls;
    const series = dailySeriesData;
    const wins = pnls.filter((p) => p > 0).length;
    const losses = pnls.filter((p) => p < 0).length;
    const netPnl = pnls.reduce((a, b) => a + b, 0);
    const invested = settledOrders.reduce((s, o) => s + orderInvested(o), 0);
    return {
      n: settledOrders.length,
      wins, losses,
      winRate: settledOrders.length > 0 ? (wins / settledOrders.length) * 100 : 0,
      netPnl,
      invested,
      roi: invested > 0 ? (netPnl / invested) * 100 : 0,
      dailyAvg: dailyAvgPnL(series),
      avgPerTrade: pnls.length > 0 ? mean(pnls) : 0,
      pnlStd: stdDev(pnls),
      sharpe: sharpeDaily(series),
      sortino: sortinoDaily(series),
      profitFactor: profitFactor(pnls),
      maxDD: maxDrawdown(series),
      days: series.length,
    };
  });

  // Per-strategy metrics from loaded orders.
  let strategyMetrics = $derived.by(() => {
    /** @type {Record<string, any[]>} */
    const byStrat = {};
    for (const o of settledOrders) {
      const s = o.strategy || 'unknown';
      if (!byStrat[s]) byStrat[s] = [];
      byStrat[s].push(o);
    }
    /** @type {Record<string, any>} */
    const out = {};
    for (const [s, os] of Object.entries(byStrat)) {
      const pnls = os.map((o) => orderPnL(o));
      const w = pnls.filter((p) => p > 0).length;
      const l = pnls.filter((p) => p < 0).length;
      const netPnl = pnls.reduce((a, b) => a + b, 0);
      const inv = os.reduce((s2, o) => s2 + orderInvested(o), 0);
      const series = dailySeries(os, (o) => o.ts, (o) => orderPnL(o));
      out[s] = {
        n: os.length, wins: w, losses: l,
        winRate: os.length > 0 ? (w / os.length) * 100 : 0,
        netPnl, invested: inv,
        roi: inv > 0 ? (netPnl / inv) * 100 : 0,
        dailyAvg: dailyAvgPnL(series),
        avgPerTrade: pnls.length > 0 ? mean(pnls) : 0,
        pnlStd: stdDev(pnls),
        sharpe: sharpeDaily(series),
        sortino: sortinoDaily(series),
        profitFactor: profitFactor(pnls),
        maxDD: maxDrawdown(series),
        days: series.length,
      };
    }
    return out;
  });

  // --- Format helpers ---
  /** @param {number} v — dollars */
  function fmtSignedDollars(v) {
    if (!v) return '$0.00';
    const sign = v < 0 ? '-' : '+';
    return `${sign}$${Math.abs(v).toFixed(2)}`;
  }
  /** @param {number} v — ratio */
  function fmtRatio(v) {
    if (v === null || v === undefined || isNaN(v)) return '\u2014';
    if (v === Infinity) return '\u221E';
    return v.toFixed(2);
  }
  /** @param {number} v — percentage */
  function fmtPctSigned(v) {
    if (v === null || v === undefined || isNaN(v)) return '\u2014';
    const sign = v > 0 ? '+' : '';
    return `${sign}${v.toFixed(1)}%`;
  }

  // --- Open positions metrics ---
  let openExposure = $derived(pendingOrders.reduce((s, o) => s + orderInvested(o), 0));
  let openAvgPrice = $derived.by(() => {
    if (pendingOrders.length === 0) return 0;
    return pendingOrders.reduce((s, o) => s + o.market_price, 0) / pendingOrders.length;
  });
  let openTotalSize = $derived(pendingOrders.reduce((s, o) => s + o.suggested_size, 0));

  // --- Tab + filter toolbar state ---
  let activeTab = $state('open');
  let filtersOpen = $state(false);
  let strategiesOpen = $state(false);

  // KPI drill-down: click a KPI to jump to filtered view
  function drillPending() { activeTab = 'open'; }
  function drillResolved() { activeTab = 'settled'; filterResult = ''; }
  function drillWins() { activeTab = 'settled'; filterResult = 'yes'; }
  function drillLosses() { activeTab = 'settled'; filterResult = 'no'; }
</script>

<svelte:head>
  <title>Paper Orders — Ghost Trader</title>
</svelte:head>

<div class="page-container wide">
  <PageHeader title="Paper Orders" {connected} error={error || ''} />

  {#if totalSummary}
    <MetricsBar
      primary={[
        { label: 'Net P&L', value: fmtPnL(totalSummary.net_pnl), tone: totalSummary.net_pnl > 0 ? 'win' : totalSummary.net_pnl < 0 ? 'loss' : null },
        { label: 'ROI', value: totalSummary.total_invested > 0 ? fmtPct((totalSummary.net_pnl / totalSummary.total_invested) * 100) : '0.0%', tone: totalSummary.total_invested > 0 && (totalSummary.net_pnl / totalSummary.total_invested) >= 0 ? 'win' : 'loss' },
        { label: 'Win Rate', value: totalSummary.resolved > 0 ? ((totalSummary.wins / totalSummary.resolved) * 100).toFixed(1) + '%' : '0.0%' },
        { label: 'Total', value: totalSummary.total_orders },
        { label: 'Pending', value: totalSummary.pending, onclick: drillPending },
        { label: 'Resolved', value: totalSummary.resolved, onclick: drillResolved },
      ]}
      secondary={[
        { label: 'Wins', value: totalSummary.wins, tone: 'win', onclick: drillWins },
        { label: 'Losses', value: totalSummary.losses, tone: 'loss', onclick: drillLosses },
        { label: 'Invested', value: '$' + totalSummary.total_invested.toFixed(2) },
        ...(riskMetrics ? [
          { label: 'Sharpe', value: fmtRatio(riskMetrics.sharpe) },
          { label: 'Sortino', value: fmtRatio(riskMetrics.sortino) },
          { label: 'Profit Factor', value: fmtRatio(riskMetrics.profitFactor) },
          { label: 'Max DD', value: '$' + riskMetrics.maxDD.toFixed(2), tone: 'loss' },
          { label: 'P&L Std', value: '$' + riskMetrics.pnlStd.toFixed(2) },
          { label: 'Days', value: riskMetrics.days },
          { label: 'Daily Avg', value: fmtSignedDollars(riskMetrics.dailyAvg), tone: riskMetrics.dailyAvg > 0 ? 'win' : riskMetrics.dailyAvg < 0 ? 'loss' : null },
          { label: 'Avg/Trade', value: fmtSignedDollars(riskMetrics.avgPerTrade), tone: riskMetrics.avgPerTrade > 0 ? 'win' : riskMetrics.avgPerTrade < 0 ? 'loss' : null },
        ] : []),
      ]}
      note={riskMetrics ? `loaded ${riskMetrics.n} settled${hasMore ? ' (sample)' : ''}` : ''}
    />
  {/if}

  {#if loading}
    <EmptyState text="Loading paper orders..." />
  {:else if error && orders.length === 0}
    <EmptyState text={error} variant="error" />
  {:else if orders.length === 0}
    <EmptyState text="No paper orders match current filters." />
  {:else}
    <!-- Filter toolbar (replaces sidebar) -->
    <div class="filter-toolbar">
      <div class="toolbar-row">
        <span class="filter-count">
          {orders.length} shown ({pendingOrders.length} pending, {settledOrders.length} settled)
          {#if hasMore}— more available{/if}
        </span>
        <button class="export-btn" onclick={() => {
          const headers = ['Time', 'Match', 'Market', 'Strategy', 'Price', 'Edge', 'Size', 'Result', 'P&L', 'Settled'];
          const rows = orders.map((o) => [
            fmtTime(o.ts),
            o.match_ticker,
            o.market_ticker,
            o.strategy,
            o.market_price,
            o.edge_cents,
            o.suggested_size,
            o.result || 'pending',
            orderPnL(o).toFixed(2),
            o.settled_ts ? fmtTime(o.settled_ts) : '',
          ]);
          exportCSV(headers, rows, `paper_orders_${Date.now()}.csv`);
        }}>Export CSV</button>
        <button class="toolbar-btn" onclick={() => (strategiesOpen = !strategiesOpen)}>
          Strategies ({selectedStrategies.size}/{strategies.length})
        </button>
        <button class="toolbar-btn" onclick={() => (filtersOpen = !filtersOpen)}>
          Filters
        </button>
        <button class="toolbar-btn" onclick={refreshInsights} disabled={insightsLoading}>
          {insightsLoading ? 'Refreshing...' : 'Refresh Insights'}
        </button>
        {#if insightsRunTS > 0}<span class="filter-count-note">insights: {new Date(insightsRunTS).toLocaleString()}</span>{/if}
      </div>
      {#if strategiesOpen}
        <div class="toolbar-panel">
          <button class="toggle-all" onclick={toggleAllStrategies}>
            {selectedStrategies.size === strategies.length ? 'Deselect All' : 'Select All'}
          </button>
          <div class="strategy-chips">
            {#each strategies as name}
              <button
                class="chip"
                class:active={selectedStrategies.has(name)}
                style="--btn-color: {colorFor(name)}"
                onclick={() => toggleStrategy(name)}
              >
                <span class="dot" style="background: {colorFor(name)}"></span>
                {name}
              </button>
            {/each}
          </div>
        </div>
      {/if}
      {#if filtersOpen}
        <div class="toolbar-panel filter-inputs">
          <label>Min Price <input type="number" bind:value={minPrice} min="0" max="1" step="0.05" placeholder="0" /></label>
          <label>Max Price <input type="number" bind:value={maxPrice} min="0" max="1" step="0.05" placeholder="0" /></label>
          <label>Match <input type="text" placeholder="Search..." bind:value={filterMatch} /></label>
          <label>Result
            <select bind:value={filterResult}>
              <option value="">All</option>
              <option value="won">Won</option>
              <option value="lost">Lost</option>
              <option value="pending">Pending</option>
            </select>
          </label>
        </div>
      {/if}
    </div>

    <PaperOrdersInsights
      bind:this={insightsComp}
      {data}
      bind:selectedStrategies
      strategyColors={strategyColors}
    />

    <Tabs
      tabs={[
        { key: 'open', label: 'Open', count: pendingOrders.length },
        { key: 'settled', label: 'Settled', count: settledOrders.length },
        { key: 'strategy', label: 'By Strategy', count: Object.keys(strategyMetrics).length },
      ]}
      bind:active={activeTab}
    />

    {#if activeTab === 'open'}
      {#if pendingOrders.length > 0}
        <div class="open-exposure-bar">
          <span class="exposure-label">Open Exposure</span>
          <span class="exposure-value">${openExposure.toFixed(2)}</span>
          <span class="exposure-stat"><span class="exposure-label">Avg Price</span><b>{(openAvgPrice * 100).toFixed(1)}c</b></span>
          <span class="exposure-stat"><span class="exposure-label">Total Size</span><b>{openTotalSize.toFixed(2)}</b></span>
          <span class="exposure-stat"><span class="exposure-label">Positions</span><b>{pendingOrders.length}</b></span>
        </div>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Match</th>
                <th>Player</th>
                <th>Strategy</th>
                <th class="num">Price</th>
                <th class="num">Size</th>
                <th class="num">Cost</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {#each pendingOrders as o}
                <tr class="row-pending clickable" onclick={() => goto(`/matches/${o.match_ticker}`)}>
                  <td class="mono">{fmtTime(o.ts)}</td>
                  <td>{fmtTicker(o.match_ticker)}</td>
                  <td>{o.player_name || o.market_ticker}</td>
                  <td>{o.strategy}</td>
                  <td class="num">{(o.market_price * 100).toFixed(0)}c</td>
                  <td class="num">{o.suggested_size.toFixed(2)}</td>
                  <td class="num">${orderInvested(o).toFixed(2)}</td>
                  <td><Badge variant="pending" text="PENDING" /></td>
                </tr>
              {/each}
            </tbody>
            <tfoot>
              <tr class="table-footer">
                <td colspan="4"><strong>{pendingOrders.length} open positions</strong></td>
                <td class="num"><strong>{(openAvgPrice * 100).toFixed(1)}c</strong></td>
                <td class="num"><strong>{openTotalSize.toFixed(2)}</strong></td>
                <td class="num"><strong>${openExposure.toFixed(2)}</strong></td>
                <td></td>
              </tr>
            </tfoot>
          </table>
        </div>
      {:else}
        <EmptyState text="No open positions." />
      {/if}
    {:else if activeTab === 'settled'}
      {#if settledOrders.length > 0}
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Match</th>
                <th>Player</th>
                <th>Strategy</th>
                <th class="num">Price</th>
                <th>Result</th>
                <th class="num">P&L</th>
                <th class="num">ROI</th>
              </tr>
            </thead>
            <tbody>
              {#each settledOrders as o}
                {@const oROI = orderROI(o)}
                <tr class={`${orderWon(o) ? 'row-win' : 'row-loss'} clickable`} onclick={() => goto(`/matches/${o.match_ticker}`)}>
                  <td class="mono">{fmtTime(o.ts)}</td>
                  <td>{fmtTicker(o.match_ticker)}</td>
                  <td>{o.player_name || o.market_ticker}</td>
                  <td>{o.strategy}</td>
                  <td class="num">{(o.market_price * 100).toFixed(0)}c</td>
                  <td>
                    <Badge variant={orderWon(o) ? 'ok' : 'err'} text={orderWon(o) ? 'WON' : 'LOST'} />
                  </td>
                  <td class="num {orderPnL(o) >= 0 ? 'pnl-win' : 'pnl-loss'}">
                    {fmtPnL(orderPnL(o))}
                  </td>
                  <td class="num">
                    {#if oROI !== null}
                      <span class:win={oROI > 0} class:loss={oROI < 0}>{fmtPctSigned(oROI)}</span>
                    {:else}
                      <span class="muted">\u2014</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
            <tfoot>
              <tr class="table-footer">
                <td colspan="5"><strong>{settledOrders.length} settled</strong> ({riskMetrics?.wins ?? 0}W / {riskMetrics?.losses ?? 0}L)</td>
                <td></td>
                <td class="num"><strong class="pnl-{(riskMetrics?.netPnl ?? 0) >= 0 ? 'win' : 'loss'}">{fmtPnL(riskMetrics?.netPnl ?? 0)}</strong></td>
                <td class="num"><strong class:win={(riskMetrics?.roi ?? 0) > 0} class:loss={(riskMetrics?.roi ?? 0) < 0}>{fmtPctSigned(riskMetrics?.roi ?? 0)}</strong></td>
              </tr>
            </tfoot>
          </table>
        </div>
        {#if hasMore}
          <div class="load-more">
            <button onclick={loadMore} disabled={loadingMore}>
              {loadingMore ? 'Loading...' : 'Load More'}
            </button>
          </div>
        {/if}
      {:else}
        <EmptyState text="No settled trades." />
      {/if}
    {:else if activeTab === 'strategy'}
      {#if Object.keys(strategyMetrics).length > 0}
        <div class="strategy-metrics-grid">
          {#each Object.entries(strategyMetrics).sort((a, b) => b[1].netPnl - a[1].netPnl) as [name, m] (name)}
            <div class="sm-card">
              <div class="sm-header">
                <span class="sm-name">{name}</span>
                <span class="sm-pnl" class:win={m.netPnl > 0} class:loss={m.netPnl < 0}>{fmtPnL(m.netPnl)}</span>
              </div>
              <div class="sm-grid">
                <div class="sm-stat"><span>N</span><b>{m.n}</b></div>
                <div class="sm-stat"><span>WR</span><b>{m.winRate.toFixed(1)}%</b></div>
                <div class="sm-stat"><span>ROI</span><b class:win={m.roi > 0} class:loss={m.roi < 0}>{fmtPctSigned(m.roi)}</b></div>
                <div class="sm-stat"><span>Sharpe</span><b>{fmtRatio(m.sharpe)}</b></div>
                <div class="sm-stat"><span>Sortino</span><b>{fmtRatio(m.sortino)}</b></div>
                <div class="sm-stat"><span>PF</span><b>{fmtRatio(m.profitFactor)}</b></div>
                <div class="sm-stat"><span>MaxDD</span><b class="loss">${m.maxDD.toFixed(2)}</b></div>
                <div class="sm-stat"><span>Daily</span><b class:win={m.dailyAvg > 0} class:loss={m.dailyAvg < 0}>{fmtSignedDollars(m.dailyAvg)}</b></div>
                <div class="sm-stat"><span>Avg/Tr</span><b class:win={m.avgPerTrade > 0} class:loss={m.avgPerTrade < 0}>{fmtSignedDollars(m.avgPerTrade)}</b></div>
                <div class="sm-stat"><span>Std</span><b>${m.pnlStd.toFixed(2)}</b></div>
                <div class="sm-stat"><span>Inv</span><b>${m.invested.toFixed(2)}</b></div>
                <div class="sm-stat"><span>Days</span><b>{m.days}</b></div>
              </div>
            </div>
          {/each}
        </div>
      {:else}
        <EmptyState text="No strategy metrics." />
      {/if}
    {/if}
  {/if}
</div>

<style>
  .filter-toolbar {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin-bottom: 16px;
    overflow: hidden;
  }
  .toolbar-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 14px;
    flex-wrap: wrap;
  }
  .toolbar-btn {
    background: var(--surface-hover);
    border: 1px solid var(--border-strong);
    color: var(--text-muted);
    padding: 5px 12px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    cursor: pointer;
  }
  .toolbar-btn:hover { color: var(--text); border-color: var(--accent); }
  .toolbar-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .toolbar-panel {
    border-top: 1px solid var(--border);
    padding: 12px 14px;
    background: var(--surface-hover);
  }
  .strategy-chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
  .chip {
    background: var(--surface);
    border: 1px solid var(--border-strong);
    color: var(--text-muted);
    padding: 4px 10px;
    border-radius: 14px;
    font-size: 12px;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .chip.active { border-color: var(--btn-color); color: var(--text); }
  .chip:hover { border-color: var(--btn-color); }
  .toggle-all {
    background: var(--surface);
    border: 1px solid var(--border-strong);
    color: var(--text-muted);
    padding: 4px 10px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    cursor: pointer;
  }
  .toggle-all:hover { background: var(--border-strong); }
  .filter-inputs { display: flex; gap: 12px; flex-wrap: wrap; }
  .filter-inputs label {
    display: flex;
    flex-direction: column;
    gap: 3px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .filter-inputs input, .filter-inputs select {
    background: var(--surface);
    border: 1px solid var(--border-strong);
    color: var(--text);
    padding: 5px 10px;
    border-radius: var(--radius-xs);
    font-size: 13px;
    min-width: 120px;
  }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; display: inline-block; }
  .filter-count { font-size: 12px; color: var(--text-muted); margin-right: auto; }
  .filter-count-note { color: var(--text-dim); font-size: 11px; }
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
  .table-wrap { overflow-x: auto; }
  .load-more { text-align: center; padding: 16px; }
  .load-more button { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 8px 24px; border-radius: var(--radius-sm); font-size: 13px; cursor: pointer; }
  .load-more button:hover { background: var(--border-strong); }
  .load-more button:disabled { opacity: 0.5; cursor: not-allowed; }
  .win { color: var(--win); }
  .loss { color: var(--loss); }
  .muted { color: var(--text-dim); }

  /* Table footer */
  .table-footer { background: var(--surface-hover); border-top: 2px solid var(--border-strong); }
  .table-footer td { font-size: 13px; padding: 10px 14px; }

  /* Open exposure bar */
  .open-exposure-bar {
    display: flex;
    align-items: center;
    gap: 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 12px 16px;
    margin-bottom: 10px;
    flex-wrap: wrap;
  }
  .exposure-label { font-size: 10px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; }
  .exposure-value { font-size: 20px; font-weight: 800; color: var(--text-bright); }
  .exposure-stat { display: flex; flex-direction: column; gap: 2px; }
  .exposure-stat b { font-size: 15px; font-weight: 700; color: var(--text-bright); }

  /* Per-strategy metrics grid */
  .strategy-metrics-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 10px;
  }
  .sm-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 12px 14px;
  }
  .sm-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; }
  .sm-name { font-size: 14px; font-weight: 700; color: var(--text-bright); }
  .sm-pnl { font-size: 16px; font-weight: 800; }
  .sm-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 4px 12px;
  }
  .sm-stat { display: flex; justify-content: space-between; font-size: 11px; }
  .sm-stat span { color: var(--text-muted); }
  .sm-stat b { color: var(--text); font-weight: 600; }
</style>
