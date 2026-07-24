<script>
  let { tabs = [], active = $bindable(''), onchange = null } = $props();
  // tabs: [{ key, label, count? }]

  function select(/** @type {string} */ key) {
    if (key === active) return;
    active = key;
    onchange?.(key);
  }
</script>

<nav class="tabs">
  {#each tabs as t (t.key)}
    <button
      class="tab"
      class:active={t.key === active}
      onclick={() => select(t.key)}
    >
      {t.label}
      {#if t.count !== undefined && t.count !== null}
        <span class="tab-count">{t.count}</span>
      {/if}
    </button>
  {/each}
</nav>

<style>
  .tabs {
    display: flex;
    gap: 2px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 16px;
  }
  .tab {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-muted);
    padding: 10px 16px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 8px;
    transition: color 0.15s, border-color 0.15s;
    margin-bottom: -1px;
  }
  .tab:hover { color: var(--text); }
  .tab.active {
    color: var(--text-bright);
    border-bottom-color: var(--accent);
  }
  .tab-count {
    font-size: 11px;
    font-weight: 700;
    color: var(--text-dim);
    background: var(--bg);
    padding: 1px 7px;
    border-radius: 10px;
  }
  .tab.active .tab-count {
    color: var(--accent);
    background: var(--loading-bg);
  }
</style>
