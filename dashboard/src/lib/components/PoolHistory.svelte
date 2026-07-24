<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '$lib/api.js';
  import { fmtTicker } from '$lib/utils.js';
  import { exportCSV } from '$lib/csv.js';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';

  let history = $state(/** @type {any[]} */ ([]));
  let loading = $state(true);
  let error = $state(/** @type {string | null} */ (null));

  async function load() {
    loading = true;
    try {
      const data = await api.getLiquidityPoolHistory(500);
      history = data?.history ?? [];
    } catch (/** @type {any} */ e) {
      error = e.message;
    }
    loading = false;
  }

  onMount(load);

  function fmtDate(/** @type {number} */ ts) {
    if (!ts) return '—';
    return new Date(ts).toLocaleString();
  }

  function fmtCents(/** @type {number} */ c) {
    if (!c) return '$0.00';
    return '$' + (c / 100).toFixed(2);
  }

  // burn rate: average daily spend
  let burnRate = $derived.by(() => {
    if (history.length < 2) return 0;
    const sorted = [...history].sort((a, b) => a.ts - b.ts);
    const first = sorted[0];
    const last = sorted[sorted.length - 1];
    const days = (last.ts - first.ts) / 86400000;
    if (days < 1) return 0;
    const spent = first.balance_before_cents - last.balance_after_cents;
    return spent / 100 / days;
  });

  let projectedDepletion = $derived.by(() => {
    if (burnRate <= 0 || history.length === 0) return null;
    const latest = history[0];
    const balance = latest.balance_after_cents / 100;
    if (balance <= 0) return null;
    return balance / burnRate;
  });

  function exportCsv() {
    if (!history.length) return;
    const headers = ['Time', 'Order ID', 'Strategy', 'Match', 'Balance Before', 'Balance After', 'Delta'];
    const rows = history.map((/** @type {any} */ h) => [
      new Date(h.ts).toISOString(),
      h.order_id,
      h.strategy,
      h.match_ticker,
      h.balance_before_cents,
      h.balance_after_cents,
      h.balance_after_cents - h.balance_before_cents,
    ]);
    exportCSV(headers, rows, `pool_history_${Date.now()}.csv`);
  }
</script>

<div class="pool-history">
  <div class="pool-stats">
    <div class="pool-stat"><span>Daily Burn</span><b class:loss={burnRate > 0}>${burnRate.toFixed(2)}/d</b></div>
    {#if projectedDepletion !== null}
      <div class="pool-stat"><span>Projected Depletion</span><b class:loss={projectedDepletion < 7}>{projectedDepletion.toFixed(0)} days</b></div>
    {/if}
    <button class="export-btn" onclick={exportCsv} disabled={!history.length}>Export CSV</button>
  </div>

  {#if loading}
    <Skeleton />
  {:else if error}
    <EmptyState text={error} variant="error" />
  {:else if history.length === 0}
    <EmptyState text="No pool history yet." />
  {:else}
    <div class="table-wrap">
      <table class="data-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Strategy</th>
            <th>Match</th>
            <th class="num">Before</th>
            <th class="num">After</th>
            <th class="num">Delta</th>
          </tr>
        </thead>
        <tbody>
          {#each history as h (h.order_id)}
            {@const delta = h.balance_after_cents - h.balance_before_cents}
            <tr>
              <td class="mono">{fmtDate(h.ts)}</td>
              <td>{h.strategy}</td>
              <td class="mono">{fmtTicker(h.match_ticker)}</td>
              <td class="num">{fmtCents(h.balance_before_cents)}</td>
              <td class="num">{fmtCents(h.balance_after_cents)}</td>
              <td class="num {delta >= 0 ? 'pnl-win' : 'pnl-loss'}">{delta >= 0 ? '+' : ''}{fmtCents(delta)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .pool-stats { display: flex; gap: 20px; margin-bottom: 16px; align-items: center; }
  .pool-stat { display: flex; flex-direction: column; gap: 2px; }
  .pool-stat span { font-size: 11px; color: var(--text-muted); text-transform: uppercase; }
  .pool-stat b { font-size: 16px; }
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
  .loss { color: var(--loss); }
  .pnl-win { color: var(--win); }
  .pnl-loss { color: var(--loss); }
</style>
