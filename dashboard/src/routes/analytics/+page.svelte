<script>
  import { api } from '$lib/api.js';
  import { fmtPnL } from '$lib/utils.js';
  import { exportCSV } from '$lib/csv.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import DateRangePicker from '$lib/components/DateRangePicker.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';

  let { data } = $props();

  let activeTab = $state('attribution');
  let attrGroup = $state('strategy');
  let range = $state('all');
  let loading = $state(false);
  let paperMetrics = $state(/** @type {any} */ (null));
  let realMetrics = $state(/** @type {any} */ (null));
  let attrData = $state(/** @type {any} */ (data?.strategy));
  let connected = $state(!data?.error);
  let error = $state(data?.error || null);

  let dateRange = $state({ fromTS: 0, toTS: 0 });

  function onRangeChange(/** @type {string} */ id, /** @type {{fromTS: number, toTS: number}} */ r) {
    dateRange = r;
    if (activeTab === 'attribution') loadAttribution();
    if (activeTab === 'pvreal') loadPaperVsReal();
    if (activeTab === 'slices') loadAttribution();
  }

  async function loadAttribution() {
    loading = true;
    try {
      attrData = await api.getOrderAttribution({
        group_by: attrGroup,
        from_ts: dateRange.fromTS || undefined,
        to_ts: dateRange.toTS || undefined,
      });
    } catch (/** @type {any} */ e) {
      error = e.message;
    }
    loading = false;
  }

  async function loadPaperVsReal() {
    loading = true;
    try {
      const [p, r] = await Promise.all([
        api.getOrderAttribution({ is_real: 'false', from_ts: dateRange.fromTS || undefined, to_ts: dateRange.toTS || undefined }),
        api.getOrderAttribution({ is_real: 'true', from_ts: dateRange.fromTS || undefined, to_ts: dateRange.toTS || undefined }),
      ]);
      paperMetrics = p;
      realMetrics = r;
    } catch (/** @type {any} */ e) {
      error = e.message;
    }
    loading = false;
  }

  function fmtPct(/** @type {number} */ v) {
    if (v === null || v === undefined || isNaN(v)) return '\u2014';
    return v.toFixed(1) + '%';
  }

  function fmtSigned(/** @type {number} */ v) {
    if (!v) return '$0.00';
    const sign = v < 0 ? '-' : '+';
    return `${sign}$${Math.abs(v).toFixed(2)}`;
  }

  function exportAttr() {
    if (!attrData?.rows) return;
    const headers = ['Group', 'Orders', 'Resolved', 'Wins', 'Losses', 'Pending', 'Win Rate %', 'Net P&L', 'Invested', 'ROI %'];
    const rows = attrData.rows.map((/** @type {any} */ r) => {
      const wr = r.resolved > 0 ? (r.wins / r.resolved) * 100 : 0;
      const roi = r.total_invested > 0 ? (r.net_pnl / r.total_invested) * 100 : 0;
      return [r.group_key, r.total_orders, r.resolved, r.wins, r.losses, r.pending, wr.toFixed(1), r.net_pnl.toFixed(2), r.total_invested.toFixed(2), roi.toFixed(1)];
    });
    exportCSV(headers, rows, `attribution_${attrGroup}_${Date.now()}.csv`);
  }

  // Merge paper + real by strategy for comparison
  let comparisonRows = $derived.by(() => {
    if (!paperMetrics?.rows || !realMetrics?.rows) return [];
    const paperMap = new Map(paperMetrics.rows.map((/** @type {any} */ r) => [r.group_key, r]));
    const realMap = new Map(realMetrics.rows.map((/** @type {any} */ r) => [r.group_key, r]));
    const keys = new Set([...paperMap.keys(), ...realMap.keys()]);
    return [...keys].map((k) => {
      const p = paperMap.get(k);
      const r = realMap.get(k);
      return {
        key: k,
        paperN: p?.total_orders ?? 0,
        paperPnL: p?.net_pnl ?? 0,
        paperWR: p?.resolved > 0 ? (p.wins / p.resolved) * 100 : 0,
        realN: r?.total_orders ?? 0,
        realPnL: r?.net_pnl ?? 0,
        realWR: r?.resolved > 0 ? (r.wins / r.resolved) * 100 : 0,
        pnlGap: (r?.net_pnl ?? 0) - (p?.net_pnl ?? 0),
      };
    }).sort((a, b) => b.realPnL - a.realPnL);
  });

  // Slice analysis: edge ranges, time-of-day
  let sliceData = $state(/** @type {any} */ (null));

  async function loadSlices() {
    loading = true;
    try {
      // Use attribution by series for ATP vs ITF
      const series = await api.getOrderAttribution({ group_by: 'series', from_ts: dateRange.fromTS || undefined, to_ts: dateRange.toTS || undefined });
      sliceData = series;
    } catch (/** @type {any} */ e) {
      error = e.message;
    }
    loading = false;
  }

  $effect(() => {
    if (activeTab === 'pvreal' && !paperMetrics) loadPaperVsReal();
    if (activeTab === 'slices' && !sliceData) loadSlices();
  });

  let total = $derived(attrData?.total);
</script>

<svelte:head>
  <title>Analytics — Ghost Trader</title>
</svelte:head>

<div class="page-container wide">
  <PageHeader title="Analytics" {connected} error={error || ''} />

  <div class="toolbar">
    <DateRangePicker bind:range onchange={onRangeChange} />
  </div>

  <Tabs
    tabs={[
      { key: 'attribution', label: 'P&L Attribution' },
      { key: 'pvreal', label: 'Paper vs Real' },
      { key: 'slices', label: 'Slices' },
    ]}
    bind:active={activeTab}
  />

  {#if activeTab === 'attribution'}
    {#if total}
      <MetricsBar
        primary={[
          { label: 'Net P&L', value: fmtPnL(total.net_pnl), tone: total.net_pnl > 0 ? 'win' : total.net_pnl < 0 ? 'loss' : null },
          { label: 'Win Rate', value: total.resolved > 0 ? ((total.wins / total.resolved) * 100).toFixed(1) + '%' : '\u2014' },
          { label: 'Orders', value: total.total_orders },
          { label: 'Resolved', value: total.resolved },
          { label: 'Pending', value: total.pending },
          { label: 'Invested', value: '$' + total.total_invested.toFixed(2) },
        ]}
      />
    {/if}

    <div class="sub-tabs">
      {#each ['strategy', 'match', 'series'] as g}
        <button class="sub-tab" class:active={attrGroup === g} onclick={() => { attrGroup = g; loadAttribution(); }}>
          By {g}
        </button>
      {/each}
      <button class="export-btn" onclick={exportAttr}>Export CSV</button>
    </div>

    {#if attrData?.rows?.length > 0}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>{attrGroup === 'strategy' ? 'Strategy' : attrGroup === 'match' ? 'Match' : 'Series'}</th>
              <th class="num">Orders</th>
              <th class="num">W</th>
              <th class="num">L</th>
              <th class="num">Win Rate</th>
              <th class="num">Net P&L</th>
              <th class="num">Invested</th>
              <th class="num">ROI</th>
            </tr>
          </thead>
          <tbody>
            {#each attrData.rows as row}
              {@const roi = row.total_invested > 0 ? (row.net_pnl / row.total_invested) * 100 : 0}
              <tr>
                <td class="mono">{row.group_key}</td>
                <td class="num">{row.total_orders}</td>
                <td class="num">{row.wins}</td>
                <td class="num">{row.losses}</td>
                <td class="num">{row.resolved > 0 ? ((row.wins / row.resolved) * 100).toFixed(1) + '%' : '\u2014'}</td>
                <td class="num {row.net_pnl >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSigned(row.net_pnl)}</td>
                <td class="num">${row.total_invested.toFixed(2)}</td>
                <td class="num" style="color: {roi >= 0 ? 'var(--win)' : 'var(--loss)'}">{roi >= 0 ? '+' : ''}{roi.toFixed(1)}%</td>
              </tr>
            {/each}
          </tbody>
          <tfoot>
            <tr class="table-footer">
              <td><strong>Total</strong></td>
              <td class="num"><strong>{total.total_orders}</strong></td>
              <td class="num"><strong>{total.wins}</strong></td>
              <td class="num"><strong>{total.losses}</strong></td>
              <td class="num"><strong>{total.resolved > 0 ? ((total.wins / total.resolved) * 100).toFixed(1) + '%' : '\u2014'}</strong></td>
              <td class="num"><strong class="{total.net_pnl >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSigned(total.net_pnl)}</strong></td>
              <td class="num"><strong>${total.total_invested.toFixed(2)}</strong></td>
              <td class="num"></td>
            </tr>
          </tfoot>
        </table>
      </div>
    {:else}
      <EmptyState text="No data for selected range." />
    {/if}
  {:else if activeTab === 'pvreal'}
    {#if comparisonRows.length > 0}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th rowspan="2">Strategy</th>
              <th colspan="3" class="group-hdr">Paper</th>
              <th colspan="3" class="group-hdr">Real</th>
              <th rowspan="2" class="num">P&L Gap</th>
            </tr>
            <tr>
              <th class="num">N</th>
              <th class="num">WR</th>
              <th class="num">P&L</th>
              <th class="num">N</th>
              <th class="num">WR</th>
              <th class="num">P&L</th>
            </tr>
          </thead>
          <tbody>
            {#each comparisonRows as row}
              <tr>
                <td class="mono">{row.key}</td>
                <td class="num">{row.paperN}</td>
                <td class="num">{row.paperWR.toFixed(1)}%</td>
                <td class="num {row.paperPnL >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSigned(row.paperPnL)}</td>
                <td class="num">{row.realN}</td>
                <td class="num">{row.realWR.toFixed(1)}%</td>
                <td class="num {row.realPnL >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSigned(row.realPnL)}</td>
                <td class="num {row.pnlGap >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSigned(row.pnlGap)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if loading}
      <EmptyState text="Loading..." />
    {:else}
      <EmptyState text="No paper vs real data." />
    {/if}
  {:else if activeTab === 'slices'}
    {#if sliceData?.rows?.length > 0}
      <CollapsibleSection title="By Series (ATP vs ITF vs Challenger)" count={sliceData.rows.length} defaultOpen={true}>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>Series</th>
                <th class="num">Orders</th>
                <th class="num">W</th>
                <th class="num">L</th>
                <th class="num">Win Rate</th>
                <th class="num">Net P&L</th>
                <th class="num">ROI</th>
              </tr>
            </thead>
            <tbody>
              {#each sliceData.rows as row}
                {@const roi = row.total_invested > 0 ? (row.net_pnl / row.total_invested) * 100 : 0}
                <tr>
                  <td class="mono">{row.group_key}</td>
                  <td class="num">{row.total_orders}</td>
                  <td class="num">{row.wins}</td>
                  <td class="num">{row.losses}</td>
                  <td class="num">{row.resolved > 0 ? ((row.wins / row.resolved) * 100).toFixed(1) + '%' : '\u2014'}</td>
                  <td class="num {row.net_pnl >= 0 ? 'pnl-win' : 'pnl-loss'}">{fmtSigned(row.net_pnl)}</td>
                  <td class="num" style="color: {roi >= 0 ? 'var(--win)' : 'var(--loss)'}">{roi >= 0 ? '+' : ''}{roi.toFixed(1)}%</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </CollapsibleSection>
    {:else if loading}
      <EmptyState text="Loading..." />
    {:else}
      <EmptyState text="No slice data." />
    {/if}
  {/if}
</div>

<style>
  .toolbar { display: flex; gap: 12px; margin-bottom: 16px; align-items: center; }
  .sub-tabs { display: flex; gap: 4px; margin-bottom: 12px; align-items: center; }
  .sub-tab {
    background: var(--surface-hover);
    border: 1px solid var(--border-strong);
    color: var(--text-muted);
    padding: 4px 12px;
    border-radius: var(--radius-xs);
    font-size: 12px;
    cursor: pointer;
  }
  .sub-tab.active { background: var(--accent); color: #fff; border-color: var(--accent); }
  .export-btn {
    margin-left: auto;
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
  .table-footer { background: var(--surface-hover); border-top: 2px solid var(--border-strong); }
  .table-footer td { font-size: 13px; padding: 10px 14px; }
  .group-hdr { text-align: center; background: var(--surface-hover); border-bottom: 1px solid var(--border); }
  .pnl-win { color: var(--win); }
  .pnl-loss { color: var(--loss); }
</style>
