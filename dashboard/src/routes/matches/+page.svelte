<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { fmtTicker, seriesFromTicker, fmtTime, fmtPnL } from '$lib/utils.js';
  import {
    dailySeries, maxDrawdown, sharpeDaily, sortinoDaily, dailyAvgPnL,
    profitFactor, mean, stdDev,
  } from '$lib/stats.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import { goto } from '$app/navigation';
  import { exportCSV } from '$lib/csv.js';

  const trackedStore = createPoll(() => api.getTracked(), 2000, { data: null, error: null, connected: false });
  const countsStore = createPoll(() => api.getOrderCounts(), 5000, { data: null, error: null, connected: false });
  const realOrdersStore = createPoll(() => api.getRealOrders(), 5000, { data: null, error: null, connected: false });
  const ordersStore = createPoll(() => api.getOrders(), 5000, { data: null, error: null, connected: false });
  const passedStore = createPoll(() => api.getPassedMatches(), 10000, { data: null, error: null, connected: false });

  let subs = $derived($trackedStore.data?.subs || []);
  let eventCount = $derived($trackedStore.data?.event_count || 0);
  let marketCount = $derived($trackedStore.data?.market_count || 0);
  /** @type {Record<string, any>} */
  let scores = $derived($trackedStore.data?.scores || {});
  /** @type {Record<string, number>} */
  let tickTS = $derived($trackedStore.data?.latest_tick_ts || {});
  /** @type {Record<string, number>} */
  let orderCounts = $derived($countsStore.data?.counts || {});
  /** @type {Record<string, number>} */
  let realOrderCounts = $derived((() => {
    /** @type {Record<string, number>} */
    const counts = {};
    for (const o of $realOrdersStore.data?.orders || []) {
      const t = o.MatchTicker || '';
      counts[t] = (counts[t] || 0) + 1;
    }
    return counts;
  })());
  let netPnl = $derived($ordersStore.data?.summary?.net_pnl ?? 0);
  let paperOrders = $derived(Object.values(orderCounts).reduce((/** @type {number} */ a, /** @type {number} */ b) => a + b, 0));
  let realOrders = $derived(Object.values(realOrderCounts).reduce((/** @type {number} */ a, /** @type {number} */ b) => a + b, 0));

  const columns = [
    { key: 'event_ticker', label: 'Event Ticker', class: 'mono' },
    { key: 'title', label: 'Match' },
    { key: 'series', label: 'Series', class: 'series' },
    { key: 'market_ticker', label: 'Market Ticker', class: 'mono' },
    { key: 'score', label: 'Score', class: 'score' },
    { key: 'sim_orders', label: 'Sim Orders', align: 'right' },
    { key: 'real_orders', label: 'Real Orders', align: 'right' },
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
      real_orders: realOrderCounts[s.event_ticker] || 0,
    }));
  })());

  // Live if API-Tennis score exists OR Kalshi tick within last 90s.
  // Score-only signal misses matches API-Tennis doesn't cover (ITF Futures,
  // Davis Cup rubbers). Tick recency catches those.
  const LIVE_TICK_WINDOW_MS = 90 * 1000;
  function isLive(/** @type {any} */ r) {
    if (scores[r.event_ticker]) return true;
    const ts = tickTS[r.event_ticker];
    if (!ts) return false;
    return Date.now() - ts < LIVE_TICK_WINDOW_MS;
  }

  let liveRows = $derived(rows.filter((/** @type {any} */ r) => isLive(r))
    .sort((/** @type {any} */ a, /** @type {any} */ b) => (a.occurrence_ts || 0) - (b.occurrence_ts || 0)));
  /** @type {any[]} */
  let nonLiveRows = $derived(rows.filter((/** @type {any} */ r) => !isLive(r))
    .sort((/** @type {any} */ a, /** @type {any} */ b) => (a.occurrence_ts || 0) - (b.occurrence_ts || 0)));

  /** @type {any[]} */
  let passedRows = $derived(($passedStore.data?.matches || []).map((/** @type {any} */ m) => ({
    ...m,
    title: m.title || fmtTicker(m.event_ticker),
    series: m.series || seriesFromTicker(m.event_ticker),
    settled: m.settled_ts ? fmtTime(m.settled_ts) : '—',
    pnl: m.net_pnl,
    pnlPerOrder: m.order_count > 0 ? m.net_pnl / m.order_count : 0,
  })).sort((/** @type {any} */ a, /** @type {any} */ b) => (b.settled_ts || 0) - (a.settled_ts || 0)));

  // --- Risk metrics from passed matches (each match = one "trade") ---
  let passedMetrics = $derived.by(() => {
    const matches = passedRows;
    if (matches.length === 0) return null;
    const pnls = matches.map((m) => m.net_pnl || 0);
    const wins = pnls.filter((p) => p > 0).length;
    const losses = pnls.filter((p) => p < 0).length;
    const netPnl = pnls.reduce((a, b) => a + b, 0);
    const totalOrders = matches.reduce((s, m) => s + (m.order_count || 0), 0);
    const series = dailySeries(matches, (m) => m.settled_ts, (m) => m.net_pnl || 0);
    return {
      n: matches.length,
      wins, losses,
      winRate: matches.length > 0 ? (wins / matches.length) * 100 : 0,
      netPnl,
      totalOrders,
      avgPerMatch: matches.length > 0 ? netPnl / matches.length : 0,
      pnlPerOrder: totalOrders > 0 ? netPnl / totalOrders : 0,
      pnlStd: stdDev(pnls),
      dailyAvg: dailyAvgPnL(series),
      sharpe: sharpeDaily(series),
      sortino: sortinoDaily(series),
      profitFactor: profitFactor(pnls),
      maxDD: maxDrawdown(series),
      days: series.length,
    };
  });

  // --- Live/upcoming aggregate stats ---
  let liveSimOrders = $derived(liveRows.reduce((s, r) => s + (r.sim_orders || 0), 0));
  let liveRealOrders = $derived(liveRows.reduce((s, r) => s + (r.real_orders || 0), 0));
  let upcomingSimOrders = $derived(nonLiveRows.reduce((s, r) => s + (r.sim_orders || 0), 0));
  let upcomingRealOrders = $derived(nonLiveRows.reduce((s, r) => s + (r.real_orders || 0), 0));

  // --- Format helpers ---
  /** @param {number} v — dollars */
  function fmtSignedDollars(v) {
    if (!v) return '$0.00';
    const sign = v < 0 ? '-' : '+';
    return `${sign}$${Math.abs(v).toFixed(2)}`;
  }
  /** @param {number} v — ratio */
  function fmtRatio(v) {
    if (v === null || v === undefined || isNaN(v)) return '—';
    if (v === Infinity) return '∞';
    return v.toFixed(2);
  }
  /** @param {number} v — percentage */
  function fmtPctSigned(v) {
    if (v === null || v === undefined || isNaN(v)) return '—';
    const sign = v > 0 ? '+' : '';
    return `${sign}${v.toFixed(1)}%`;
  }

  function handleRowClick(/** @type {any} */ row) {
    goto(`/matches/${encodeURIComponent(row.event_ticker)}`);
  }

  let activeTab = $state('live');
</script>

<svelte:head>
  <title>Tracked Matches — Ghost Trader</title>
</svelte:head>

<div class="page-container">
  <PageHeader title="Tracked Matches" connected={$trackedStore.connected} error={$trackedStore.error || ''} />

  <MetricsBar
    primary={[
      { label: 'Live', value: liveRows.length },
      { label: 'Upcoming', value: nonLiveRows.length },
      { label: 'Passed', value: passedRows.length },
      { label: 'Events', value: eventCount },
      { label: 'Paper', value: paperOrders },
      { label: 'Real', value: realOrders },
    ]}
    secondary={[
      { label: 'Markets', value: marketCount },
      { label: 'Live Sim', value: liveSimOrders },
      { label: 'Live Real', value: liveRealOrders },
      ...(passedMetrics ? [
        { label: 'Passed P&L', value: fmtPnL(passedMetrics.netPnl), tone: passedMetrics.netPnl > 0 ? 'win' : passedMetrics.netPnl < 0 ? 'loss' : null },
        { label: 'Win Rate', value: passedMetrics.winRate.toFixed(1) + '%' },
        { label: 'Sharpe', value: fmtRatio(passedMetrics.sharpe) },
        { label: 'Sortino', value: fmtRatio(passedMetrics.sortino) },
        { label: 'PF', value: fmtRatio(passedMetrics.profitFactor) },
        { label: 'Max DD', value: '$' + passedMetrics.maxDD.toFixed(2), tone: 'loss' },
        { label: 'P&L/Order', value: fmtSignedDollars(passedMetrics.pnlPerOrder), tone: passedMetrics.pnlPerOrder > 0 ? 'win' : passedMetrics.pnlPerOrder < 0 ? 'loss' : null },
        { label: 'Days', value: passedMetrics.days },
      ] : []),
    ]}
  />

  {#if $trackedStore.connected && subs.length === 0}
    <EmptyState text="No matches currently tracked." />
  {:else if !$trackedStore.connected}
    <EmptyState text="Cannot reach ghost-trader on :6060. Is it running?" variant="error" />
  {:else}
    <div class="match-toolbar">
      <button class="export-btn" onclick={() => {
        const headers = ['Event Ticker', 'Match', 'Series', 'Score', 'Sim Orders', 'Live Orders'];
        const rowsData = rows.map((r) => [
          r.event_ticker,
          r.title,
          r.series,
          r.score,
          r.sim_orders,
          r.real_orders,
        ]);
        exportCSV(headers, rowsData, `tracked_matches_${Date.now()}.csv`);
      }}>Export CSV</button>
    </div>

    <Tabs
      tabs={[
        { key: 'live', label: 'Live', count: liveRows.length },
        { key: 'upcoming', label: 'Upcoming', count: nonLiveRows.length },
        { key: 'passed', label: 'Passed', count: passedRows.length },
      ]}
      bind:active={activeTab}
    />

    {#if activeTab === 'live'}
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
            <tfoot>
              <tr class="table-footer">
                <td colspan="5"><strong>{liveRows.length} live</strong></td>
                <td class="num"><strong>{liveSimOrders}</strong> sim</td>
                <td class="num"><strong>{liveRealOrders}</strong> real</td>
              </tr>
            </tfoot>
          </table>
        </div>
      {:else}
        <EmptyState text="No live matches." />
      {/if}
    {:else if activeTab === 'upcoming'}
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
            <tfoot>
              <tr class="table-footer">
                <td colspan="5"><strong>{nonLiveRows.length} upcoming</strong></td>
                <td class="num"><strong>{upcomingSimOrders}</strong> sim</td>
                <td class="num"><strong>{upcomingRealOrders}</strong> real</td>
                <td></td>
              </tr>
            </tfoot>
          </table>
        </div>
      {:else}
        <EmptyState text="No upcoming matches." />
      {/if}
    {:else if activeTab === 'passed'}
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
                <th class="num">P&L/Order</th>
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
                  <td class="num {row.pnlPerOrder >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSignedDollars(row.pnlPerOrder)}</td>
                </tr>
              {/each}
            </tbody>
            <tfoot>
              <tr class="table-footer">
                <td colspan="5"><strong>{passedRows.length} passed</strong> ({passedMetrics?.wins ?? 0}W / {passedMetrics?.losses ?? 0}L)</td>
                <td class="num"><strong>{passedMetrics?.totalOrders ?? 0}</strong></td>
                <td class="num"><strong class="pnl-{(passedMetrics?.netPnl ?? 0) >= 0 ? 'win' : 'loss'}">{fmtPnL(passedMetrics?.netPnl ?? 0)}</strong></td>
                <td class="num"><strong class="pnl-{(passedMetrics?.pnlPerOrder ?? 0) >= 0 ? 'win' : 'loss'}">{fmtSignedDollars(passedMetrics?.pnlPerOrder ?? 0)}</strong></td>
              </tr>
            </tfoot>
          </table>
        </div>
      {:else}
        <EmptyState text="No passed matches." />
      {/if}
    {/if}
  {/if}
</div>

<style>
  .win { color: var(--win); }
  .loss { color: var(--loss); }
  .table-footer { background: var(--surface-hover); border-top: 2px solid var(--border-strong); }
  .table-footer td { font-size: 13px; padding: 10px 14px; }
  .match-toolbar { display: flex; justify-content: flex-end; margin-bottom: 10px; }
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
</style>
