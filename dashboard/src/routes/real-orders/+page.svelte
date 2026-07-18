<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import { goto } from '$app/navigation';

  const ordersStore = createPoll(() => api.getRealOrders(), 5000, { data: null, error: null, connected: false });
  const poolStore = createPoll(() => api.getLiquidityPool(), 5000, { data: null, error: null, connected: false });

  let ordersData = $derived($ordersStore.data);
  let poolData = $derived($poolStore.data);
  let connected = $derived($ordersStore.connected);
  let error = $derived($ordersStore.error);

  let filterStatus = $state('all');

  /** @type {any[]} */
  let orders = $derived(ordersData?.orders ?? []);
  let pool = $derived(poolData);

  let filteredOrders = $derived.by(() => {
    if (filterStatus === 'all') return orders;
    return orders.filter((/** @type {any} */ o) => o.OrderStatus === filterStatus);
  });

  let summary = $derived.by(() => {
    const total = orders.length;
    const filled = orders.filter((/** @type {any} */ o) => o.OrderStatus === 'filled' || o.OrderStatus === 'partial').length;
    const resolved = orders.filter((/** @type {any} */ o) => o.OrderStatus === 'resolved').length;
    const failed = orders.filter((/** @type {any} */ o) => o.OrderStatus === 'failed').length;
    const pnl = orders.reduce((/** @type {number} */ sum, /** @type {any} */ o) => sum + (o.ResolvedPNLCents || 0), 0);
    return { total, filled, resolved, failed, pnl };
  });

  /** @param {number} c */
  function fmtCents(c) {
    if (!c) return '$0.00';
    return `$${(c / 100).toFixed(2)}`;
  }

  /** @param {number} ts */
  function fmtTime(ts) {
    if (!ts) return '';
    return new Date(ts).toLocaleTimeString();
  }

  /** @param {string} s */
  function statusClass(s) {
    if (s === 'filled' || s === 'resolved') return 'badge-ok';
    if (s === 'failed') return 'badge-err';
    if (s === 'submitted' || s === 'partial') return 'badge-loading';
    return 'badge-pending';
  }

  function handleRowClick(/** @type {any} */ o) {
    goto(`/matches/${encodeURIComponent(o.MatchTicker)}`);
  }
</script>

<svelte:head><title>Real Orders — Kalshi Ghost Trader</title></svelte:head>

<div class="page-container">
  <PageHeader title="Real Orders" {connected} {error} />

  {#if pool}
    <div class="summary-bar" style="margin-bottom: 20px;">
      <div class="summary-stat">
        <span class="label">Pool Balance</span>
        <span class="value">{fmtCents(pool.balance_cents)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Initial Balance</span>
        <span class="value">{fmtCents(pool.initial_balance_cents)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Total Spent</span>
        <span class="value">{fmtCents(pool.total_spent_cents)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Total P&L</span>
        <span class="value" class:win={pool.total_pnl_cents > 0} class:loss={pool.total_pnl_cents < 0}>
          {fmtCents(pool.total_pnl_cents)}
        </span>
      </div>
    </div>
  {/if}

  <div class="summary-bar" style="margin-bottom: 20px;">
    <div class="summary-stat">
      <span class="label">Total Orders</span>
      <span class="value">{summary.total}</span>
    </div>
    <div class="summary-stat">
      <span class="label">Filled</span>
      <span class="value">{summary.filled}</span>
    </div>
    <div class="summary-stat">
      <span class="label">Resolved</span>
      <span class="value">{summary.resolved}</span>
    </div>
    <div class="summary-stat">
      <span class="label">Failed</span>
      <span class="value">{summary.failed}</span>
    </div>
    <div class="summary-stat">
      <span class="label">Realized P&L</span>
      <span class="value" class:win={summary.pnl > 0} class:loss={summary.pnl < 0}>
        {fmtCents(summary.pnl)}
      </span>
    </div>
  </div>

  <div class="filters">
    <select bind:value={filterStatus}>
      <option value="all">All Status</option>
      <option value="submitted">Submitted</option>
      <option value="filled">Filled</option>
      <option value="partial">Partial</option>
      <option value="resolved">Resolved</option>
      <option value="failed">Failed</option>
    </select>
    <span class="filter-count">{filteredOrders.length} orders</span>
  </div>

  {#if filteredOrders.length === 0}
    <EmptyState text="No real orders yet" />
  {:else}
    <CollapsibleSection title="Real Orders" count={filteredOrders.length}>
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
              <th class="num">Price</th>
              <th class="num">Fill</th>
              <th>Status</th>
              <th>Order ID</th>
              <th class="num">P&L</th>
            </tr>
          </thead>
          <tbody>
            {#each filteredOrders as o (o.ID)}
              <tr class="clickable" onclick={() => handleRowClick(o)}
                  class:row-win={o.OrderStatus === 'resolved' && o.ResolvedPNLCents > 0}
                  class:row-loss={o.OrderStatus === 'resolved' && o.ResolvedPNLCents < 0}>
                <td class="mono">{fmtTime(o.TS)}</td>
                <td>{o.MatchTitle || o.MatchTicker}</td>
                <td>{o.PlayerName || o.MarketTicker}</td>
                <td>{o.Strategy}</td>
                <td>{o.Action}</td>
                <td class="num">{o.SuggestedSize?.toFixed(2)}</td>
                <td class="num">{o.MarketPrice?.toFixed(4)}</td>
                <td class="num">{o.FillCount?.toFixed(2) ?? '-'}</td>
                <td><span class="badge {statusClass(o.OrderStatus)}">{o.OrderStatus || 'pending'}</span></td>
                <td class="mono">{o.KalshiOrderID || '-'}</td>
                <td class="num">
                  {#if o.ResolvedPNLCents}
                    <span class:win={o.ResolvedPNLCents > 0} class:loss={o.ResolvedPNLCents < 0}>
                      {fmtCents(o.ResolvedPNLCents)}
                    </span>
                  {:else}
                    -
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </CollapsibleSection>
  {/if}
</div>

<style>
  .win { color: var(--win); }
  .loss { color: var(--loss); }
</style>
