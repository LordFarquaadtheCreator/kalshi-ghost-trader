<script>
  let { range = $bindable('7d'), onchange = null } = $props();

  const presets = [
    { id: '1d', label: '24h', days: 1 },
    { id: '7d', label: '7d', days: 7 },
    { id: '30d', label: '30d', days: 30 },
    { id: '90d', label: '90d', days: 90 },
    { id: 'ytd', label: 'YTD', days: 0 },
    { id: 'all', label: 'All', days: -1 },
  ];

  function select(/** @type {string} */ id) {
    range = id;
    onchange?.(id, getRange());
  }

  export function getRange() {
    const p = presets.find((p) => p.id === range);
    if (!p || p.days === -1) return { fromTS: 0, toTS: 0 };
    if (p.days === 0) {
      const start = new Date(new Date().getFullYear(), 0, 1).getTime();
      return { fromTS: start, toTS: 0 };
    }
    const fromTS = Date.now() - p.days * 86400000;
    return { fromTS, toTS: 0 };
  }
</script>

<div class="date-range">
  {#each presets as p (p.id)}
    <button class="preset" class:active={range === p.id} onclick={() => select(p.id)}>
      {p.label}
    </button>
  {/each}
</div>

<style>
  .date-range { display: flex; gap: 4px; }
  .preset {
    background: var(--surface-hover);
    border: 1px solid var(--border-strong);
    color: var(--text-muted);
    padding: 4px 10px;
    border-radius: var(--radius-xs);
    font-size: 12px;
    cursor: pointer;
    transition: all 0.15s;
  }
  .preset:hover { color: var(--text); border-color: var(--accent); }
  .preset.active {
    background: var(--accent);
    color: #fff;
    border-color: var(--accent);
  }
</style>
