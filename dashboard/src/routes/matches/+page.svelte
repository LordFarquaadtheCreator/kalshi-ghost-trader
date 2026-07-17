<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { fmtTicker, seriesFromTicker } from '$lib/utils.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import { goto } from '$app/navigation';

  const trackedStore = createPoll(() => api.getTracked(), 2000, { data: null, error: null, connected: false });
  const countsStore = createPoll(() => api.getOrderCounts(), 5000, { data: null, error: null, connected: false });
  const ordersStore = createPoll(() => api.getOrders(), 5000, { data: null, error: null, connected: false });

  let subs = $derived($trackedStore.data?.subs || []);
  let eventCount = $derived($trackedStore.data?.event_count || 0);
  let marketCount = $derived($trackedStore.data?.market_count || 0);
  /** @type {Record<string, number>} */
  let orderCounts = $derived($countsStore.data?.counts || {});
  let netPnl = $derived(ordersStore.data?.summary?.net_pnl ?? 0);
  let paperOrders = $derived(ordersStore.data?.summary?.total_orders ?? 0);
  let realOrders = $derived(0);

  const columns = [
    { key: 'event_ticker', label: 'Event Ticker', class: 'mono' },
    { key: 'match', label: 'Match' },
    { key: 'series', label: 'Series', class: 'series' },
    { key: 'market_ticker', label: 'Market Ticker', class: 'mono' },
    { key: 'sim_orders', label: 'Sim Orders', align: 'right' },
    { key: 'real_orders', label: 'Real Orders', align: 'right', class: 'num muted' },
  ];

  let rows = $derived(subs.map((/** @type {any} */ s) => ({
    ...s,
    match: fmtTicker(s.event_ticker),
    series: seriesFromTicker(s.event_ticker),
    sim_orders: orderCounts[s.event_ticker] || 0,
    real_orders: 0,
  })));

  function handleRowClick(/** @type {any} */ row) {
    goto(`/matches/${encodeURIComponent(row.event_ticker)}`);
  }
</script>

<svelte:head>
  <title>Tracked Matches — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="Tracked Matches" connected={$trackedStore.connected} error={$trackedStore.error || ''} />

  <div class="stats-grid">
    <StatCard label="Events" value={eventCount} />
    <StatCard label="Markets" value={marketCount} />
    <StatCard label="Net P&L" value={`$${netPnl.toFixed(2)}`} />
    <StatCard label="Paper Orders" value={paperOrders} />
    <StatCard label="Real Orders" value={realOrders} />
  </div>

  {#if $trackedStore.connected && subs.length === 0}
    <EmptyState text="No matches currently tracked." />
  {:else if !$trackedStore.connected}
    <EmptyState text="Cannot reach ghost-trader on :6060. Is it running?" variant="error" />
  {:else}
    <CollapsibleSection title="Tracked Markets" count={subs.length}>
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              {#each columns as col}
                <th class={col.align === 'right' ? 'num' : ''}>{col.label}</th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each rows as row}
              <tr class="clickable" onclick={() => handleRowClick(row)}>
                {#each columns as col}
                  <td class={col.class || (col.align === 'right' ? 'num' : '')}>{row[col.key]}</td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </CollapsibleSection>
  {/if}
</div>
