<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { fmtTicker, seriesFromTicker, fmtTime, fmtPnL } from '$lib/utils.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import { goto } from '$app/navigation';

  const trackedStore = createPoll(() => api.getTracked(), 2000, { data: null, error: null, connected: false });
  const countsStore = createPoll(() => api.getOrderCounts(), 5000, { data: null, error: null, connected: false });
  const pendingStore = createPoll(() => api.getPendingOrderCounts(), 5000, { data: null, error: null, connected: false });
  const ordersStore = createPoll(() => api.getOrders(), 5000, { data: null, error: null, connected: false });
  const passedStore = createPoll(() => api.getPassedMatches(), 10000, { data: null, error: null, connected: false });

  let subs = $derived($trackedStore.data?.subs || []);
  let eventCount = $derived($trackedStore.data?.event_count || 0);
  let marketCount = $derived($trackedStore.data?.market_count || 0);
  /** @type {Record<string, any>} */
  let scores = $derived($trackedStore.data?.scores || {});
  /** @type {Record<string, number>} */
  let orderCounts = $derived($countsStore.data?.counts || {});
  let pendingCounts = $derived($pendingStore.data?.counts || {});
  let netPnl = $derived($ordersStore.data?.summary?.net_pnl ?? 0);
  let paperOrders = $derived(Object.values(orderCounts).reduce((/** @type {number} */ a, /** @type {number} */ b) => a + b, 0));
  let liveOrders = $derived(Object.values(pendingCounts).reduce((/** @type {number} */ a, /** @type {number} */ b) => a + b, 0));

  const columns = [
    { key: 'event_ticker', label: 'Event Ticker', class: 'mono' },
    { key: 'title', label: 'Match' },
    { key: 'series', label: 'Series', class: 'series' },
    { key: 'market_ticker', label: 'Market Ticker', class: 'mono' },
    { key: 'score', label: 'Score', class: 'score' },
    { key: 'sim_orders', label: 'Sim Orders', align: 'right' },
    { key: 'live_orders', label: 'Live Orders', align: 'right' },
  ];

  function fmtScore(/** @type {any} */ s) {
    if (!s) return '—';
    const sets = `${s.home_set_games}-${s.away_set_games}`;
    const games = `${s.home_games}-${s.away_games}`;
    const pts = `${s.home_points}-${s.away_points}`;
    return `${sets}  ${games}  ${pts}`;
  }

  function fmtStarts(/** @type {number} */ ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    const now = new Date();
    const diffMs = ts - now.getTime();
    if (diffMs <= 0) return 'started';
    const diffMin = Math.round(diffMs / 60000);
    if (diffMin < 60) return `in ${diffMin}m`;
    const diffHr = Math.round(diffMin / 60);
    if (diffHr < 24) return `in ${diffHr}h`;
    return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  let rows = $derived((() => {
    const byEvent = new Map();
    for (const s of subs) {
      const existing = byEvent.get(s.event_ticker);
      if (existing) {
        existing.market_ticker = `${existing.market_ticker}, ${s.market_ticker}`;
        existing.subscribed_at = Math.max(existing.subscribed_at, s.subscribed_at || 0);
      } else {
        byEvent.set(s.event_ticker, { ...s });
      }
    }
    return Array.from(byEvent.values()).map((/** @type {any} */ s) => ({
      ...s,
      title: s.title || fmtTicker(s.event_ticker),
      series: seriesFromTicker(s.event_ticker),
      score: fmtScore(scores[s.event_ticker]),
      sim_orders: orderCounts[s.event_ticker] || 0,
      live_orders: pendingCounts[s.event_ticker] || 0,
    }));
  })());

  let liveRows = $derived(rows.filter((/** @type {any} */ r) => scores[r.event_ticker])
    .sort((/** @type {any} */ a, /** @type {any} */ b) => (a.occurrence_ts || 0) - (b.occurrence_ts || 0)));
  /** @type {any[]} */
  let nonLiveRows = $derived(rows.filter((/** @type {any} */ r) => !scores[r.event_ticker])
    .sort((/** @type {any} */ a, /** @type {any} */ b) => (a.occurrence_ts || 0) - (b.occurrence_ts || 0)));

  /** @type {any[]} */
  let passedRows = $derived(($passedStore.data?.matches || []).map((/** @type {any} */ m) => ({
    ...m,
    title: m.title || fmtTicker(m.event_ticker),
    series: m.series || seriesFromTicker(m.event_ticker),
    settled: m.settled_ts ? fmtTime(m.settled_ts) : '—',
    pnl: m.net_pnl,
  })).sort((/** @type {any} */ a, /** @type {any} */ b) => (b.settled_ts || 0) - (a.settled_ts || 0)));

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
    <StatCard label="Live Orders" value={liveOrders} />
  </div>

  {#if $trackedStore.connected && subs.length === 0}
    <EmptyState text="No matches currently tracked." />
  {:else if !$trackedStore.connected}
    <EmptyState text="Cannot reach ghost-trader on :6060. Is it running?" variant="error" />
  {:else}
    <CollapsibleSection title="Live Matches" count={liveRows.length}>
      {#if liveRows.length > 0}
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
              {#each liveRows as row}
                <tr class="clickable" onclick={() => handleRowClick(row)}>
                  {#each columns as col}
                    <td class={col.class || (col.align === 'right' ? 'num' : '')}>{row[col.key]}</td>
                  {/each}
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <EmptyState text="No live matches." />
      {/if}
    </CollapsibleSection>

    <CollapsibleSection title="Upcoming Matches" count={nonLiveRows.length} defaultOpen={false}>
      {#if nonLiveRows.length > 0}
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                {#each columns as col}
                  <th class={col.align === 'right' ? 'num' : ''}>{col.label}</th>
                {/each}
                <th>Starts</th>
              </tr>
            </thead>
            <tbody>
              {#each nonLiveRows as row}
                <tr class="clickable" onclick={() => handleRowClick(row)}>
                  {#each columns as col}
                    <td class={col.class || (col.align === 'right' ? 'num' : '')}>{row[col.key]}</td>
                  {/each}
                  <td>{fmtStarts(row.occurrence_ts)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <EmptyState text="No upcoming matches." />
      {/if}
    </CollapsibleSection>

    <CollapsibleSection title="Passed Matches" count={passedRows.length} defaultOpen={false}>
      {#if passedRows.length > 0}
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>Event Ticker</th>
                <th>Match</th>
                <th>Series</th>
                <th>Winner</th>
                <th>Settled</th>
                <th class="num">Sim Orders</th>
                <th class="num">Net P&L</th>
              </tr>
            </thead>
            <tbody>
              {#each passedRows as row}
                <tr class="clickable" onclick={() => handleRowClick(row)}>
                  <td class="mono">{row.event_ticker}</td>
                  <td>{row.title}</td>
                  <td class="series">{row.series}</td>
                  <td>{row.winner || '—'}</td>
                  <td>{row.settled}</td>
                  <td class="num">{row.order_count}</td>
                  <td class="num {row.pnl >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtPnL(row.pnl)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <EmptyState text="No passed matches." />
      {/if}
    </CollapsibleSection>
  {/if}
</div>
