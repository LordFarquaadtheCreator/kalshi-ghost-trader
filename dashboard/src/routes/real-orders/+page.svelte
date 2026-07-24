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
  let resetDollars = $state('');
  let topupDollars = $state('');
  let poolMsg = $state(null); // { type: 'ok' | 'err', text: string }

  /** @type {any} */
  let metricsData = $state(null);
  let metricsDay = $state(''); // empty = aggregate
  let metricsLoading = $state(false);

  /** @param {string} day */
  async function loadMetrics(day) {
    metricsLoading = true;
    try {
      metricsData = await api.getRealOrderMetrics(day);
    } catch (e) {
      metricsData = null;
    } finally {
      metricsLoading = false;
    }
  }

  $effect(() => {
    loadMetrics(metricsDay);
  });

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

  async function handleReset() {
    const dollars = parseFloat(resetDollars);
    if (!dollars || dollars <= 0) {
      poolMsg = { type: 'err', text: 'Enter a positive dollar amount' };
      return;
    }
    if (!confirm(`Reset pool to $${dollars.toFixed(2)}? Wipes total_spent and total_pnl.`)) return;
    try {
      await api.resetLiquidityPool(Math.round(dollars * 100));
      poolMsg = { type: 'ok', text: `Pool reset to $${dollars.toFixed(2)}` };
      resetDollars = '';
    } catch (e) {
      poolMsg = { type: 'err', text: `Reset failed: ${e.message}` };
    }
  }

  async function handleTopUp() {
    const dollars = parseFloat(topupDollars);
    if (!dollars || dollars <= 0) {
      poolMsg = { type: 'err', text: 'Enter a positive dollar amount' };
      return;
    }
    try {
      await api.topUpLiquidityPool(Math.round(dollars * 100));
      poolMsg = { type: 'ok', text: `Topped up $${dollars.toFixed(2)}` };
      topupDollars = '';
    } catch (e) {
      poolMsg = { type: 'err', text: `Top-up failed: ${e.message}` };
    }
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

    <div class="pool-controls">
      <div class="pool-control-group">
        <input type="number" bind:value={resetDollars} placeholder="20.00" step="0.01" min="0" />
        <button class="btn btn-warn" onclick={handleReset}>Reset Pool ($)</button>
      </div>
      <div class="pool-control-group">
        <input type="number" bind:value={topupDollars} placeholder="5.00" step="0.01" min="0" />
        <button class="btn" onclick={handleTopUp}>Top Up ($)</button>
      </div>
      {#if poolMsg}
        <span class="pool-msg" class:ok={poolMsg.type === 'ok'} class:err={poolMsg.type === 'err'}>
          {poolMsg.text}
        </span>
      {/if}
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

  {#if metricsData}
    <CollapsibleSection title="Per-Strategy Metrics" count={metricsData.strategies?.length ?? 0} defaultOpen={true}>
      <div class="metrics-filters">
        <select bind:value={metricsDay}>
          <option value="">Aggregate (all days)</option>
          {#each (metricsData.days ?? []) as d}
            <option value={d}>{d}</option>
          {/each}
        </select>
        {#if metricsLoading}<span class="filter-count">loading…</span>{/if}
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Strategy</th>
              <th class="num">Orders</th>
              <th class="num">Filled</th>
              <th class="num">Resolved</th>
              <th class="num">Wins</th>
              <th class="num">Losses</th>
              <th class="num">Pending</th>
              <th class="num">Canceled</th>
              <th class="num">Win Rate</th>
              <th class="num">Invested</th>
              <th class="num">Net P&L</th>
              <th class="num">ROI</th>
              <th class="num">Avg Price</th>
              <th class="num">Avg Edge</th>
            </tr>
          </thead>
          <tbody>
            {#each (metricsData.strategies ?? []) as m (m.strategy)}
              <tr>
                <td>{m.strategy}</td>
                <td class="num">{m.total_orders}</td>
                <td class="num">{m.filled}</td>
                <td class="num">{m.resolved}</td>
                <td class="num">{m.wins}</td>
                <td class="num">{m.losses}</td>
                <td class="num">{m.pending}</td>
                <td class="num">{m.canceled}</td>
                <td class="num">{winRate(m)}</td>
                <td class="num">${m.total_invested?.toFixed(2)}</td>
                <td class="num">
                  <span class:win={m.net_pnl_cents > 0} class:loss={m.net_pnl_cents < 0}>
                    {fmtCents(m.net_pnl_cents)}
                  </span>
                </td>
                <td class="num">{roi(m)}</td>
                <td class="num">{m.avg_price?.toFixed(3)}</td>
                <td class="num">{m.avg_edge?.toFixed(1)}¢</td>
              </tr>
            {/each}
            {#if metricsData.total}
              <tr class="total-row">
                <td><strong>Total</strong></td>
                <td class="num"><strong>{metricsData.total.total_orders}</strong></td>
                <td class="num"><strong>{metricsData.total.filled}</strong></td>
                <td class="num"><strong>{metricsData.total.resolved}</strong></td>
                <td class="num"><strong>{metricsData.total.wins}</strong></td>
                <td class="num"><strong>{metricsData.total.losses}</strong></td>
                <td class="num"><strong>{metricsData.total.pending}</strong></td>
                <td class="num"><strong>{metricsData.total.canceled}</strong></td>
                <td class="num"><strong>{winRate(metricsData.total)}</strong></td>
                <td class="num"><strong>${metricsData.total.total_invested?.toFixed(2)}</strong></td>
                <td class="num">
                  <strong class:win={metricsData.total.net_pnl_cents > 0} class:loss={metricsData.total.net_pnl_cents < 0}>
                    {fmtCents(metricsData.total.net_pnl_cents)}
                  </strong>
                </td>
                <td class="num"><strong>{roi(metricsData.total)}</strong></td>
                <td class="num"><strong>{metricsData.total.avg_price?.toFixed(3)}</strong></td>
                <td class="num"><strong>{metricsData.total.avg_edge?.toFixed(1)}¢</strong></td>
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    </CollapsibleSection>
  {/if}

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
              <th>Context</th>
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
                <td class="ctx">{o.Context || '-'}</td>
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
  .ctx { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .pool-controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    margin-bottom: 20px;
    padding: 12px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .pool-control-group {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .pool-control-group input {
    width: 90px;
    padding: 4px 8px;
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 4px;
    font: inherit;
  }
  .btn {
    padding: 4px 12px;
    background: var(--accent, #3b82f6);
    color: white;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font: inherit;
  }
  .btn:hover { filter: brightness(1.1); }
  .btn-warn { background: var(--loss, #ef4444); }
  .pool-msg { font-size: 0.9em; }
  .pool-msg.ok { color: var(--win); }
  .pool-msg.err { color: var(--loss); }

  .metrics-filters {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 12px;
  }
  .metrics-filters select {
    padding: 4px 8px;
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 4px;
    font: inherit;
  }
  .total-row {
    border-top: 2px solid var(--border-strong);
    background: var(--surface);
  }
</style>
