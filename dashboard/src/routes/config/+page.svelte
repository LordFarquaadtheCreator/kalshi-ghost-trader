<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { onMount } from 'svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';

  const configStore = createPoll(() => api.getAppConfig(), 30000, { data: null, error: null, connected: false });
  const strategyStore = createPoll(() => api.getStrategyConfig(), 30000, { data: null, error: null, connected: false });
  const triggerStore = createPoll(() => api.getTriggerRanges(), 30000, { data: null, error: null, connected: false });

  /** @type {any[]} */
  let configHistory = $state([]);
  let historyLoading = $state(true);

  async function loadHistory() {
    historyLoading = true;
    try {
      const data = await api.getAppConfigHistory(50);
      configHistory = data?.history ?? [];
    } catch {
      configHistory = [];
    }
    historyLoading = false;
  }
  onMount(loadHistory);

  function fmtHistoryDate(/** @type {number} */ ts) {
    if (!ts) return '\u2014';
    return new Date(ts).toLocaleString();
  }

  let configData = $derived($configStore.data);
  let strategyData = $derived($strategyStore.data);
  let triggerData = $derived($triggerStore.data);
  let connected = $derived($configStore.connected);
  let error = $derived($configStore.error);

  /** @type {any[]} */
  let configPairs = $derived(configData?.config ?? []);
  /** @type {any[]} */
  let strategies = $derived(strategyData?.strategies ?? []);
  /** @type {Record<string, any[]>} */
  let triggerRanges = $derived(triggerData?.ranges ?? {});

  // --- Aggregate stats ---
  let enabledCount = $derived(strategies.filter((s) => s.Enabled).length);
  let disabledCount = $derived(strategies.length - enabledCount);
  let totalBands = $derived.by(() => {
    let n = 0;
    for (const arr of Object.values(triggerRanges)) n += arr.length;
    return n;
  });
  let activeBands = $derived.by(() => {
    let n = 0;
    for (const arr of Object.values(triggerRanges)) n += arr.filter((r) => r.enabled).length;
    return n;
  });
  let strategiesWithBands = $derived.by(() => {
    let n = 0;
    for (const arr of Object.values(triggerRanges)) if (arr.length > 0) n++;
    return n;
  });
  let strategiesWithLimits = $derived(strategies.filter((s) => s.PerMarketMaxOrders > 0).length);

  let editingKey = $state('');
  let editingValue = $state('');
  let saveMsg = $state('');
  let toggleMsg = $state('');
  let bandMsg = $state('');

  // Per-market strategy limit editing state
  /** @type {Record<string, string>} */
  let limitInputs = $state({});

  // Price band editing state
  /** @type {Record<string, {min: string, max: string}>} */
  let bandInputs = $state({});
  /** @type {string|null} */
  let expandedBandStrategy = $state(null);

  async function saveConfig() {
    if (!editingKey.trim()) return;
    try {
      await api.setAppConfig(editingKey.trim(), editingValue.trim());
      saveMsg = `Saved ${editingKey}`;
      editingKey = '';
      editingValue = '';
      setTimeout(() => { saveMsg = ''; }, 3000);
    } catch (/** @type {any} */ err) {
      saveMsg = `Error: ${err.message}`;
    }
  }

  async function toggleStrategy(/** @type {string} */ name, /** @type {boolean} */ current) {
    try {
      await api.setStrategyEnabled(name, !current);
      toggleMsg = `${name}: ${!current ? 'enabled' : 'disabled'}`;
      setTimeout(() => { toggleMsg = ''; }, 3000);
    } catch (/** @type {any} */ err) {
      toggleMsg = `Error: ${err.message}`;
    }
  }

  async function saveLimit(/** @type {string} */ name) {
    const raw = limitInputs[name] ?? '';
    const parsed = parseInt(raw, 10);
    if (isNaN(parsed) || parsed < 0) {
      toggleMsg = 'Invalid limit: must be non-negative integer';
      setTimeout(() => { toggleMsg = ''; }, 3000);
      return;
    }
    try {
      await api.setStrategyLimit(name, parsed);
      toggleMsg = `${name}: per-market limit set to ${parsed}`;
      limitInputs[name] = '';
      setTimeout(() => { toggleMsg = ''; }, 3000);
    } catch (/** @type {any} */ err) {
      toggleMsg = `Error: ${err.message}`;
    }
  }

  function toggleBandSection(/** @type {string} */ name) {
    expandedBandStrategy = expandedBandStrategy === name ? null : name;
    if (expandedBandStrategy === name && !bandInputs[name]) {
      bandInputs[name] = { min: '', max: '' };
    }
  }

  async function addBand(/** @type {string} */ strategy) {
    const input = bandInputs[strategy];
    if (!input || !input.min || !input.max) return;
    const min = parseFloat(input.min);
    const max = parseFloat(input.max);
    if (isNaN(min) || isNaN(max) || min >= max || min < 0 || max > 1) {
      bandMsg = 'Invalid range: 0 < min < max <= 1';
      setTimeout(() => { bandMsg = ''; }, 3000);
      return;
    }
    const existing = triggerRanges[strategy] ?? [];
    const newRange = { min_price: min, max_price: max, source: 'manual', enabled: true };
    try {
      await api.replaceTriggerRanges(strategy, [...existing, newRange]);
      bandInputs[strategy] = { min: '', max: '' };
      bandMsg = `Added band [${min}, ${max}] for ${strategy}`;
      setTimeout(() => { bandMsg = ''; }, 3000);
    } catch (/** @type {any} */ err) {
      bandMsg = `Error: ${err.message}`;
    }
  }

  async function removeBand(/** @type {string} */ strategy, /** @type {number} */ index) {
    const existing = triggerRanges[strategy] ?? [];
    const updated = existing.filter((_, i) => i !== index);
    try {
      await api.replaceTriggerRanges(strategy, updated);
      bandMsg = `Removed band from ${strategy}`;
      setTimeout(() => { bandMsg = ''; }, 3000);
    } catch (/** @type {any} */ err) {
      bandMsg = `Error: ${err.message}`;
    }
  }

  async function toggleBand(/** @type {string} */ strategy, /** @type {number} */ index) {
    const existing = triggerRanges[strategy] ?? [];
    const updated = existing.map((r, i) => i === index ? { ...r, enabled: !r.enabled } : r);
    try {
      await api.replaceTriggerRanges(strategy, updated);
    } catch (/** @type {any} */ err) {
      bandMsg = `Error: ${err.message}`;
    }
  }
</script>

<svelte:head><title>Config — Kalshi Ghost Trader</title></svelte:head>

<div class="page-container">
  <PageHeader title="Configuration" {connected} {error} />

  {#if strategies.length > 0}
    <MetricsBar
      primary={[
        { label: 'Strategies', value: strategies.length },
        { label: 'Enabled', value: enabledCount, tone: 'win' },
        { label: 'Disabled', value: disabledCount, tone: 'loss' },
        { label: 'Bands', value: totalBands },
        { label: 'Active Bands', value: activeBands, tone: 'win' },
        { label: 'Config Keys', value: configPairs.length },
      ]}
      secondary={[
        { label: 'With Limits', value: strategiesWithLimits },
        { label: 'With Bands', value: strategiesWithBands },
        { label: 'Inactive Bands', value: totalBands - activeBands },
      ]}
    />
  {/if}

  {#if toggleMsg}
    <div class="msg-bar">{toggleMsg}</div>
  {/if}

  <CollapsibleSection title="Strategy Toggles" count={strategies.length}>
    {#if strategies.length === 0}
      <EmptyState text="No strategy config entries" />
    {:else}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Strategy</th>
              <th>Enabled</th>
              <th>Per-Market Limit</th>
              <th>Updated</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {#each strategies as s (s.Strategy)}
              <tr>
                <td class="mono">{s.Strategy}</td>
                <td>
                  <span class="badge {s.Enabled ? 'badge-ok' : 'badge-pending'}">
                    {s.Enabled ? 'enabled' : 'disabled'}
                  </span>
                </td>
                <td class="mono">
                  {s.PerMarketMaxOrders > 0 ? s.PerMarketMaxOrders : 'none'}
                  <input
                    type="number"
                    min="0"
                    step="1"
                    placeholder="set"
                    class="config-input limit-input"
                    bind:value={limitInputs[s.Strategy]}
                  />
                  <button class="toggle-btn limit-save-btn" onclick={() => saveLimit(s.Strategy)}>Set</button>
                </td>
                <td class="mono">{new Date(s.UpdatedTS).toLocaleString()}</td>
                <td>
                  <button class="toggle-btn" onclick={() => toggleStrategy(s.Strategy, s.Enabled)}>
                    {s.Enabled ? 'Disable' : 'Enable'}
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
          <tfoot>
            <tr class="table-footer">
              <td><strong>{strategies.length} strategies</strong></td>
              <td><strong>{enabledCount} enabled / {disabledCount} disabled</strong></td>
              <td><strong>{strategiesWithLimits} with limits</strong></td>
              <td colspan="2"></td>
            </tr>
          </tfoot>
        </table>
      </div>
    {/if}
  </CollapsibleSection>

  {#if bandMsg}
    <div class="msg-bar">{bandMsg}</div>
  {/if}

  <CollapsibleSection title="Price Bands" count={strategies.length}>
    {#if strategies.length === 0}
      <EmptyState text="No strategy config entries" />
    {:else}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Strategy</th>
              <th>Enabled</th>
              <th>Bands</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {#each strategies as s (s.Strategy)}
              <tr>
                <td class="mono">{s.Strategy}</td>
                <td>
                  <span class="badge {s.Enabled ? 'badge-ok' : 'badge-pending'}">
                    {s.Enabled ? 'enabled' : 'disabled'}
                  </span>
                </td>
                <td class="mono">
                  {(triggerRanges[s.Strategy] ?? []).filter(r => r.enabled).length}
                  / {(triggerRanges[s.Strategy] ?? []).length} active
                </td>
                <td>
                  <button class="toggle-btn" onclick={() => toggleBandSection(s.Strategy)}>
                    {expandedBandStrategy === s.Strategy ? 'Close' : 'Manage'}
                  </button>
                </td>
              </tr>
              {#if expandedBandStrategy === s.Strategy}
                <tr class="band-detail-row">
                  <td colspan="4">
                    <div class="band-detail">
                      <div class="band-add-row">
                        <input
                          type="number"
                          step="0.01"
                          min="0"
                          max="1"
                          placeholder="min price"
                          class="config-input band-input"
                          bind:value={bandInputs[s.Strategy].min}
                        />
                        <input
                          type="number"
                          step="0.01"
                          min="0"
                          max="1"
                          placeholder="max price"
                          class="config-input band-input"
                          bind:value={bandInputs[s.Strategy].max}
                        />
                        <button class="save-btn" onclick={() => addBand(s.Strategy)}>Add Band</button>
                      </div>
                      {#if (triggerRanges[s.Strategy] ?? []).length === 0}
                        <div class="band-empty">No price bands configured</div>
                      {:else}
                        <table class="data-table band-table">
                          <thead>
                            <tr>
                              <th>Min</th>
                              <th>Max</th>
                              <th>Source</th>
                              <th>Enabled</th>
                              <th>Actions</th>
                            </tr>
                          </thead>
                          <tbody>
                            {#each triggerRanges[s.Strategy] as band, i (i)}
                              <tr>
                                <td class="mono">{band.min_price.toFixed(2)}</td>
                                <td class="mono">{band.max_price.toFixed(2)}</td>
                                <td class="mono">{band.source}</td>
                                <td>
                                  <span class="badge {band.enabled ? 'badge-ok' : 'badge-pending'}">
                                    {band.enabled ? 'on' : 'off'}
                                  </span>
                                </td>
                                <td>
                                  <button class="toggle-btn band-action-btn" onclick={() => toggleBand(s.Strategy, i)}>
                                    {band.enabled ? 'Disable' : 'Enable'}
                                  </button>
                                  <button class="toggle-btn band-action-btn" onclick={() => removeBand(s.Strategy, i)}>
                                    Remove
                                  </button>
                                </td>
                              </tr>
                            {/each}
                          </tbody>
                          <tfoot>
                            <tr class="table-footer">
                              <td colspan="3"><strong>{(triggerRanges[s.Strategy] ?? []).length} bands</strong> ({(triggerRanges[s.Strategy] ?? []).filter(r => r.enabled).length} active)</td>
                              <td colspan="2"></td>
                            </tr>
                          </tfoot>
                        </table>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
          <tfoot>
            <tr class="table-footer">
              <td><strong>{strategies.length} strategies</strong></td>
              <td></td>
              <td><strong>{totalBands} bands ({activeBands} active)</strong></td>
              <td></td>
            </tr>
          </tfoot>
        </table>
      </div>
    {/if}
  </CollapsibleSection>

  {#if saveMsg}
    <div class="msg-bar">{saveMsg}</div>
  {/if}

  <CollapsibleSection title="App Config" count={configPairs.length} defaultOpen={false}>
    <div class="config-edit">
      <input bind:value={editingKey} placeholder="key" class="config-input" />
      <input bind:value={editingValue} placeholder="value" class="config-input" />
      <button class="save-btn" onclick={saveConfig}>Save</button>
    </div>

    {#if configPairs.length === 0}
      <EmptyState text="No config entries" />
    {:else}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Key</th>
              <th>Value</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {#each configPairs as pair (pair.Key)}
              <tr>
                <td class="mono">{pair.Key}</td>
                <td class="mono">{pair.Value}</td>
                <td>
                  <button class="toggle-btn" onclick={() => { editingKey = pair.Key; editingValue = pair.Value; }}>
                    Edit
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
          <tfoot>
            <tr class="table-footer">
              <td><strong>{configPairs.length} config keys</strong></td>
              <td colspan="2"></td>
            </tr>
          </tfoot>
        </table>
      </div>
    {/if}
  </CollapsibleSection>

  <CollapsibleSection title="Config History" count={configHistory.length} defaultOpen={false}>
    {#if historyLoading}
      <EmptyState text="Loading..." />
    {:else if configHistory.length === 0}
      <EmptyState text="No config changes recorded." />
    {:else}
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Time</th>
              <th>Key</th>
              <th>Action</th>
              <th>Old Value</th>
              <th>New Value</th>
            </tr>
          </thead>
          <tbody>
            {#each configHistory as h (h.ID)}
              <tr>
                <td class="mono">{fmtHistoryDate(h.ChangedTS)}</td>
                <td class="mono">{h.Key}</td>
                <td><span class="badge badge-{h.Action === 'delete' ? 'err' : 'ok'}">{h.Action}</span></td>
                <td class="mono">{h.OldValue || '\u2014'}</td>
                <td class="mono">{h.NewValue || '\u2014'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </CollapsibleSection>
</div>

<style>
  .table-footer { background: var(--surface-hover); border-top: 2px solid var(--border-strong); }
  .table-footer td { font-size: 13px; padding: 10px 14px; }
  .msg-bar {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 8px 14px;
    margin-bottom: 16px;
    font-size: 13px;
    color: var(--accent);
  }
  .config-edit {
    display: flex;
    gap: 8px;
    margin-bottom: 16px;
  }
  .config-input {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--text);
    padding: 6px 10px;
    font-size: 13px;
    font-family: var(--font-mono);
    flex: 1;
  }
  .save-btn, .toggle-btn {
    background: var(--surface-hover);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    color: var(--text);
    padding: 6px 14px;
    font-size: 13px;
    cursor: pointer;
  }
  .save-btn:hover, .toggle-btn:hover {
    background: var(--border-strong);
  }
  .band-detail-row > td {
    padding: 12px 16px !important;
    background: var(--surface);
  }
  .band-detail {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .band-add-row {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .band-input {
    max-width: 120px;
  }
  .band-table {
    margin-top: 4px;
  }
  .band-table th,
  .band-table td {
    padding: 4px 10px;
    font-size: 12px;
  }
  .band-action-btn {
    padding: 3px 10px;
    font-size: 12px;
    margin-right: 4px;
  }
  .band-empty {
    color: var(--text-dim);
    font-size: 13px;
    padding: 8px 0;
  }
  .limit-input {
    max-width: 70px;
    display: inline-block;
    margin: 0 6px;
  }
  .limit-save-btn {
    padding: 3px 10px;
    font-size: 12px;
  }
</style>
