<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { fmtTime, fmtTicker, seriesFromTicker, fmtPnL, fmtPct } from '$lib/utils.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';

  const store = createPoll(() => api.getOrders(), 5000, { data: null, error: null, connected: false });

  let data = $derived($store.data);
  let loading = $derived(!$store.data && $store.connected === false && !$store.error);
  let filterStrategy = $state('');
  let filterResult = $state('');

  /** @type {any[]} */
  let filteredOrders = $derived.by(() => {
    if (!data || !data.orders) return [];
    return data.orders.filter((/** @type {any} */ o) => {
      if (filterStrategy && o.strategy !== filterStrategy) return false;
      if (filterResult === 'won' && !o.won) return false;
      if (filterResult === 'lost' && o.won) return false;
      if (filterResult === 'pending' && o.result) return false;
      return true;
    });
  });

  let settledOrders = $derived(filteredOrders.filter((/** @type {any} */ o) => o.result));
  let pendingOrders = $derived(filteredOrders.filter((/** @type {any} */ o) => !o.result));

  let filteredSummary = $derived.by(() => {
    /** @type {{ total: number, resolved: number, wins: number, losses: number, pending: number, win_rate: number, invested: number, net_pnl: number, roi: number }} */
    const s = { total: 0, resolved: 0, wins: 0, losses: 0, pending: 0, win_rate: 0, invested: 0, net_pnl: 0, roi: 0 };
    for (const o of filteredOrders) {
      s.total++;
      s.invested += o.suggested_size * o.market_price;
      if (o.result) {
        s.resolved++;
        if (o.won) { s.wins++; s.net_pnl += o.suggested_size * (1.0 - o.market_price); }
        else { s.losses++; s.net_pnl += -o.suggested_size * o.market_price; }
      } else {
        s.pending++;
      }
    }
    if (s.resolved > 0) s.win_rate = (s.wins / s.resolved) * 100;
    if (s.invested > 0) s.roi = (s.net_pnl / s.invested) * 100;
    return s;
  });

  let strategies = $derived.by(() => {
    if (!data || !data.orders) return [];
    return [...new Set(data.orders.map((/** @type {any} */ o) => o.strategy))].sort();
  });
</script>

<svelte:head>
  <title>Paper Orders — Ghost Trader</title>
</svelte:head>

<div class="page-container wide">
  <PageHeader title="Paper Orders" connected={$store.connected} error={$store.error || ''} />

  {#if data}
    <div class="summary-bar">
      <div class="summary-stat">
        <span class="label">Total</span>
        <span class="value">{filteredSummary.total}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Resolved</span>
        <span class="value">{filteredSummary.resolved}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Wins</span>
        <span class="value value-win">{filteredSummary.wins}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Losses</span>
        <span class="value value-loss">{filteredSummary.losses}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Pending</span>
        <span class="value">{filteredSummary.pending}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Win Rate</span>
        <span class="value">{filteredSummary.win_rate.toFixed(1)}%</span>
      </div>
      <div class="summary-stat">
        <span class="label">Invested</span>
        <span class="value">${filteredSummary.invested.toFixed(2)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Net P&L</span>
        <span class="value {filteredSummary.net_pnl >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPnL(filteredSummary.net_pnl)}
        </span>
      </div>
      <div class="summary-stat">
        <span class="label">ROI</span>
        <span class="value {filteredSummary.roi >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPct(filteredSummary.roi)}
        </span>
      </div>
    </div>
  {/if}

  {#if loading}
    <EmptyState text="Loading paper orders..." />
  {:else if $store.error}
    <EmptyState text={$store.error} variant="error" />
  {:else if !data || data.orders.length === 0}
    <EmptyState text="No paper orders yet." />
  {:else}
    <div class="filters">
      <select bind:value={filterStrategy}>
        <option value="">All Strategies</option>
        {#each strategies as s}
          <option value={s}>{s}</option>
        {/each}
      </select>
      <select bind:value={filterResult}>
        <option value="">All Results</option>
        <option value="won">Won</option>
        <option value="lost">Lost</option>
        <option value="pending">Pending</option>
      </select>
      <span class="filter-count">{filteredOrders.length} shown ({settledOrders.length} settled, {pendingOrders.length} pending)</span>
    </div>

    {#if pendingOrders.length > 0}
      <h2 class="section-title">Open Positions — {pendingOrders.length}</h2>
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
              <tr class="row-pending">
                <td class="mono">{fmtTime(o.ts)}</td>
                <td>{fmtTicker(o.match_ticker)}</td>
                <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                <td>{o.player_name || o.market_ticker}</td>
                <td>{o.context}</td>
                <td>{o.strategy}</td>
                <td>{(o.market_price * 100).toFixed(0)}c</td>
                <td>{o.edge_cents}c</td>
                <td>{o.suggested_size}</td>
                <td><Badge variant="pending" text="PENDING" /></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if settledOrders.length > 0}
      <h2 class="section-title">Settled Trades — {settledOrders.length}</h2>
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
              <tr class={o.won ? 'row-win' : 'row-loss'}>
                <td class="mono">{fmtTime(o.ts)}</td>
                <td>{fmtTicker(o.match_ticker)}</td>
                <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                <td>{o.player_name || o.market_ticker}</td>
                <td>{o.context}</td>
                <td>{o.strategy}</td>
                <td>{(o.market_price * 100).toFixed(0)}c</td>
                <td>{o.edge_cents}c</td>
                <td>{o.suggested_size}</td>
                <td>
                  <Badge variant={o.won ? 'ok' : 'err'} text={o.won ? 'WON' : 'LOST'} />
                </td>
                <td class={o.pnl >= 0 ? 'pnl-win' : 'pnl-loss'}>
                  {fmtPnL(o.pnl)}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if pendingOrders.length === 0 && settledOrders.length === 0}
      <EmptyState text="No orders match current filters." />
    {/if}
  {/if}
</div>

<style>
  .section-title { font-size: 16px; font-weight: 600; color: var(--text-bright); margin: 20px 0 10px; }
</style>
