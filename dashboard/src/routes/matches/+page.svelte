<script>
  import { onMount, onDestroy } from 'svelte';
  import { browser } from '$app/environment';

  const API_URL = 'http://127.0.0.1:6060';
  const POLL_MS = 2000;

  /** @type {{market_ticker: string, event_ticker: string}[]}} */
  let subs = $state([]);
  let eventCount = $state(0);
  let marketCount = $state(0);
  let connected = $state(false);
  let error = $state('');
  /** @type {ReturnType<typeof setInterval> | null} */
  let timer = null;

  async function poll() {
    try {
      const res = await fetch(`${API_URL}/api/tracked`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      subs = data.subs || [];
      eventCount = data.event_count || 0;
      marketCount = data.market_count || 0;
      connected = true;
      error = '';
    } catch (err) {
      connected = false;
      error = err instanceof Error ? err.message : String(err);
    }
  }

  /** @param {string} t */
  function fmtTicker(t) {
    if (!t) return '';
    const parts = t.split('-');
    if (parts.length <= 1) return t;
    return parts.slice(1).join(' vs ');
  }

  /** @param {string} t */
  function seriesFromTicker(t) {
    if (!t) return '';
    const idx = t.indexOf('-');
    return idx > 0 ? t.substring(0, idx) : '';
  }

  onMount(() => {
    if (browser) {
      poll();
      timer = setInterval(poll, POLL_MS);
    }
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });
</script>

<svelte:head>
  <title>Tracked Matches — Ghost Trader</title>
</svelte:head>

<div class="matches">
  <header>
    <h1>Tracked Matches</h1>
    <div class="status">
      {#if connected}
        <span class="badge ok">Connected</span>
      {:else}
        <span class="badge err">Disconnected</span>
      {/if}
      {#if error}
        <span class="error">{error}</span>
      {/if}
    </div>
  </header>

  <div class="summary">
    <div class="stat-card">
      <div class="stat-label">Events</div>
      <div class="stat-value">{eventCount}</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">Markets</div>
      <div class="stat-value">{marketCount}</div>
    </div>
  </div>

  {#if connected && subs.length === 0}
    <div class="empty">No matches currently tracked.</div>
  {:else if !connected}
    <div class="empty">Cannot reach ghost-trader on :6060. Is it running?</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Event Ticker</th>
          <th>Match</th>
          <th>Series</th>
          <th>Market Ticker</th>
        </tr>
      </thead>
      <tbody>
        {#each subs as sub}
          <tr>
            <td class="mono">{sub.event_ticker}</td>
            <td>{fmtTicker(sub.event_ticker)}</td>
            <td class="series">{seriesFromTicker(sub.event_ticker)}</td>
            <td class="mono">{sub.market_ticker}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .matches {
    max-width: 1400px;
    margin: 0 auto;
    padding: 20px;
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 20px;
  }

  h1 {
    font-size: 22px;
    font-weight: 700;
    margin: 0;
    color: #f1f5f9;
  }

  .status {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .badge {
    padding: 4px 10px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
  }

  .badge.ok {
    background: #064e3b;
    color: #34d399;
  }

  .badge.err {
    background: #450a0a;
    color: #f87171;
  }

  .error {
    font-size: 12px;
    color: #f87171;
  }

  .summary {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
    gap: 10px;
    margin-bottom: 20px;
  }

  .stat-card {
    background: #0f172a;
    border: 1px solid #1e293b;
    border-radius: 8px;
    padding: 12px;
  }

  .stat-label {
    font-size: 11px;
    color: #64748b;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .stat-value {
    font-size: 20px;
    font-weight: 700;
    color: #f1f5f9;
    margin-top: 4px;
  }

  .empty {
    background: #0f172a;
    border: 1px solid #1e293b;
    border-radius: 8px;
    padding: 40px;
    text-align: center;
    color: #64748b;
    font-size: 14px;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    background: #0f172a;
    border: 1px solid #1e293b;
    border-radius: 8px;
    overflow: hidden;
  }

  thead {
    background: #1e293b;
  }

  th {
    text-align: left;
    padding: 10px 14px;
    font-size: 11px;
    color: #94a3b8;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    font-weight: 600;
  }

  td {
    padding: 10px 14px;
    font-size: 13px;
    color: #e2e8f0;
    border-top: 1px solid #1e293b;
  }

  .mono {
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
    color: #94a3b8;
  }

  .series {
    color: #60a5fa;
    font-size: 12px;
  }

  tbody tr:hover {
    background: #1e293b;
  }
</style>
