<script>
  import { api } from '$lib/api.js';
  import { fmtTime, fmtTicker, seriesFromTicker, fmtPnL, fmtPct, vibrantColor } from '$lib/utils.js';
  import { browser } from '$app/environment';
  import { goto } from '$app/navigation';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import PaperOrdersInsights from '$lib/components/PaperOrdersInsights.svelte';

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
</script>

<svelte:head>
  <title>Paper Orders — Ghost Trader</title>
</svelte:head>

<div class="page-container wide">
  <PageHeader title="Paper Orders" {connected} error={error || ''} />

  {#if totalSummary}
    <div class="summary-bar">
      <div class="summary-stat">
        <span class="label">Total</span>
        <span class="value">{totalSummary.total_orders}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Resolved</span>
        <span class="value">{totalSummary.resolved}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Wins</span>
        <span class="value value-win">{totalSummary.wins}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Losses</span>
        <span class="value value-loss">{totalSummary.losses}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Pending</span>
        <span class="value">{totalSummary.pending}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Win Rate</span>
        <span class="value">{totalSummary.resolved > 0 ? ((totalSummary.wins / totalSummary.resolved) * 100).toFixed(1) : '0.0'}%</span>
      </div>
      <div class="summary-stat">
        <span class="label">Invested</span>
        <span class="value">${totalSummary.total_invested.toFixed(2)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Net P&L</span>
        <span class="value {totalSummary.net_pnl >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPnL(totalSummary.net_pnl)}
        </span>
      </div>
      <div class="summary-stat">
        <span class="label">ROI</span>
        <span class="value {totalSummary.total_invested > 0 && (totalSummary.net_pnl / totalSummary.total_invested) >= 0 ? 'value-win' : 'value-loss'}">
          {totalSummary.total_invested > 0 ? fmtPct((totalSummary.net_pnl / totalSummary.total_invested) * 100) : '0.0%'}
        </span>
      </div>
    </div>
  {/if}

  {#if loading}
    <EmptyState text="Loading paper orders..." />
  {:else if error && orders.length === 0}
    <EmptyState text={error} variant="error" />
  {:else if orders.length === 0}
    <EmptyState text="No paper orders match current filters." />
  {:else}
    <div class="layout">
      <div class="main-content">
        <div class="filter-count">
          {orders.length} shown ({pendingOrders.length} pending, {settledOrders.length} settled)
          {#if hasMore}— more available{/if}
          {#if insightsRunTS > 0}<span class="filter-count-note"> (insights: {new Date(insightsRunTS).toLocaleString()})</span>{/if}
        </div>

        <PaperOrdersInsights
          bind:this={insightsComp}
          {data}
          bind:selectedStrategies
          strategyColors={strategyColors}
        />

        {#if pendingOrders.length > 0}
          <CollapsibleSection title="Open Positions" count={pendingOrders.length}>
            <div class="table-wrap">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Match</th>
                    <th>Series</th>
                    <th>Player</th>
                    <th>Context</th>
                    <th>Strategy</th>
                    <th>Price</th>
                    <th>Edge</th>
                    <th>Size</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {#each pendingOrders as o}
                    <tr class="row-pending clickable" onclick={() => goto(`/matches/${o.match_ticker}`)}>
                      <td class="mono">{fmtTime(o.ts)}</td>
                      <td>{fmtTicker(o.match_ticker)}</td>
                      <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                      <td>{o.player_name || o.market_ticker}</td>
                      <td>{o.context}</td>
                      <td>{o.strategy}</td>
                      <td>{(o.market_price * 100).toFixed(0)}c</td>
                      <td>{o.edge_cents}c</td>
                      <td>{o.suggested_size.toFixed(2)}</td>
                      <td><Badge variant="pending" text="PENDING" /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        {/if}

        {#if settledOrders.length > 0}
          <CollapsibleSection title="Settled Trades" count={settledOrders.length}>
            <div class="table-wrap">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Match</th>
                    <th>Series</th>
                    <th>Player</th>
                    <th>Context</th>
                    <th>Strategy</th>
                    <th>Price</th>
                    <th>Edge</th>
                    <th>Size</th>
                    <th>Result</th>
                    <th>P&L</th>
                  </tr>
                </thead>
                <tbody>
                  {#each settledOrders as o}
                    <tr class={`${orderWon(o) ? 'row-win' : 'row-loss'} clickable`} onclick={() => goto(`/matches/${o.match_ticker}`)}>
                      <td class="mono">{fmtTime(o.ts)}</td>
                      <td>{fmtTicker(o.match_ticker)}</td>
                      <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                      <td>{o.player_name || o.market_ticker}</td>
                      <td>{o.context}</td>
                      <td>{o.strategy}</td>
                      <td>{(o.market_price * 100).toFixed(0)}c</td>
                      <td>{o.edge_cents}c</td>
                      <td>{o.suggested_size.toFixed(2)}</td>
                      <td>
                        <Badge variant={orderWon(o) ? 'ok' : 'err'} text={orderWon(o) ? 'WON' : 'LOST'} />
                      </td>
                      <td class={orderPnL(o) >= 0 ? 'pnl-win' : 'pnl-loss'}>
                        {fmtPnL(orderPnL(o))}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        {/if}

        {#if hasMore}
          <div class="load-more">
            <button onclick={loadMore} disabled={loadingMore}>
              {loadingMore ? 'Loading...' : 'Load More'}
            </button>
          </div>
        {/if}
      </div>

      <aside class="filter-sidebar">
        <div class="filter-group">
          <h3>Strategies</h3>
          <button class="toggle-all" onclick={toggleAllStrategies}>
            {selectedStrategies.size === strategies.length ? 'Deselect All' : 'Select All'}
          </button>
          <div class="strategy-list">
            {#each strategies as name}
              <button
                class="toggle-btn"
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

        <div class="filter-group">
          <h3>Filters</h3>
          <label class="filter-label">Min Price
            <input type="number" bind:value={minPrice} min="0" max="1" step="0.05" placeholder="0 (off)" />
          </label>
          <label class="filter-label">Max Price
            <input type="number" bind:value={maxPrice} min="0" max="1" step="0.05" placeholder="0 (off)" />
          </label>
          <label class="filter-label">Match
            <input type="text" placeholder="Search match..." bind:value={filterMatch} />
          </label>
          <label class="filter-label">Result
            <select bind:value={filterResult}>
              <option value="">All Results</option>
              <option value="won">Won</option>
              <option value="lost">Lost</option>
              <option value="pending">Pending</option>
            </select>
          </label>
        </div>

        <div class="filter-group">
          <h3>Insights</h3>
          <button class="run-btn" onclick={refreshInsights} disabled={insightsLoading}>
            {insightsLoading ? 'Refreshing...' : 'Refresh Insights'}
          </button>
        </div>
      </aside>
    </div>
  {/if}
</div>

<style>
  .layout { display: flex; gap: 20px; align-items: flex-start; }
  .main-content { flex: 1; min-width: 0; }
  .filter-sidebar { width: 240px; flex-shrink: 0; position: sticky; top: 16px; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; max-height: calc(100vh - 32px); overflow-y: auto; }
  .filter-group { margin-bottom: 20px; }
  .filter-group:last-child { margin-bottom: 0; }
  .filter-group h3 { font-size: 12px; font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; margin: 0 0 10px 0; }
  .strategy-list { display: flex; flex-direction: column; gap: 6px; margin-bottom: 8px; }
  .toggle-all { background: var(--surface-hover); border: 1px solid var(--border-strong); color: #94a3b8; padding: 6px 12px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; margin-bottom: 8px; width: 100%; text-align: left; }
  .toggle-all:hover { background: var(--border-strong); }
  .toggle-btn { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text-muted); padding: 6px 10px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; display: flex; align-items: center; gap: 6px; transition: all 0.15s; text-align: left; }
  .toggle-btn.active { border-color: var(--btn-color); color: var(--text); }
  .toggle-btn:hover { border-color: var(--btn-color); }
  .filter-label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--text-muted); margin-bottom: 10px; }
  .filter-label input, .filter-label select { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 5px 10px; border-radius: var(--radius-xs); font-size: 13px; width: 100%; box-sizing: border-box; }
  .run-btn { background: #1e40af; border: 1px solid #3b82f6; color: var(--text); padding: 6px 16px; border-radius: var(--radius-sm); font-size: 13px; font-weight: 600; cursor: pointer; width: 100%; }
  .run-btn:hover { background: #2563eb; }
  .run-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; display: inline-block; }
  .filter-count { font-size: 12px; color: var(--text-muted); margin-bottom: 16px; }
  .filter-count-note { color: var(--text-dim); }
  .table-wrap { overflow-x: auto; }
  .load-more { text-align: center; padding: 16px; }
  .load-more button { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 8px 24px; border-radius: var(--radius-sm); font-size: 13px; cursor: pointer; }
  .load-more button:hover { background: var(--border-strong); }
  .load-more button:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
