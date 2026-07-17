<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';

  const configStore = createPoll(() => api.getAppConfig(), 30000, { data: null, error: null, connected: false });
  const strategyStore = createPoll(() => api.getStrategyConfig(), 30000, { data: null, error: null, connected: false });

  let configData = $derived($configStore.data);
  let strategyData = $derived($strategyStore.data);
  let connected = $derived($configStore.connected);
  let error = $derived($configStore.error);

  /** @type {any[]} */
  let configPairs = $derived(configData?.config ?? []);
  /** @type {any[]} */
  let strategies = $derived(strategyData?.strategies ?? []);

  let editingKey = $state('');
  let editingValue = $state('');
  let saveMsg = $state('');
  let toggleMsg = $state('');

  async function saveConfig() {
    if (!editingKey.trim()) return;
    try {
      await api.setAppConfig(editingKey.trim(), editingValue.trim());
      saveMsg = `Saved ${editingKey}`;
      editingKey = '';
      editingValue = '';
      // Force refresh by clearing cache — next poll cycle picks it up
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
</script>

<svelte:head><title>Config — Kalshi Ghost Trader</title></svelte:head>

<div class="page-container">
  <PageHeader title="Configuration" {connected} {error} />

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
                <td class="mono">{new Date(s.UpdatedTS).toLocaleString()}</td>
                <td>
                  <button class="toggle-btn" onclick={() => toggleStrategy(s.Strategy, s.Enabled)}>
                    {s.Enabled ? 'Disable' : 'Enable'}
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
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
        </table>
      </div>
    {/if}
  </CollapsibleSection>
</div>

<style>
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
</style>
