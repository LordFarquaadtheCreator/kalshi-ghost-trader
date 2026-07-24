<script>
  let { primary = [], secondary = [], note = '' } = $props();
  let expanded = $state(false);
</script>

<div class="metrics-bar" class:expanded>
  <div class="metrics-row">
    {#each primary as k}
      <div class="kpi" class:win={k.tone === 'win'} class:loss={k.tone === 'loss'} class:clickable={!!k.onclick} role={k.onclick ? 'button' : undefined} tabindex={k.onclick ? 0 : undefined} onclick={k.onclick} onkeydown={k.onclick ? (e) => e.key === 'Enter' && k.onclick() : undefined}>
        <span class="kpi-label">{k.label}</span>
        <span class="kpi-value">{k.value}</span>
      </div>
    {/each}
    {#if secondary.length > 0}
      <button class="more-toggle" onclick={() => (expanded = !expanded)}>
        {expanded ? 'Less' : `+${secondary.length}`}
      </button>
    {/if}
    {#if note}<span class="metrics-note">{note}</span>{/if}
  </div>
  {#if expanded && secondary.length > 0}
    <div class="metrics-row secondary">
      {#each secondary as k}
        <div class="kpi" class:win={k.tone === 'win'} class:loss={k.tone === 'loss'} class:clickable={!!k.onclick} role={k.onclick ? 'button' : undefined} tabindex={k.onclick ? 0 : undefined} onclick={k.onclick} onkeydown={k.onclick ? (e) => e.key === 'Enter' && k.onclick() : undefined}>
          <span class="kpi-label">{k.label}</span>
          <span class="kpi-value">{k.value}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .metrics-bar {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin-bottom: 16px;
    overflow: hidden;
  }
  .metrics-row {
    display: flex;
    gap: 0;
    align-items: center;
    overflow-x: auto;
  }
  .metrics-row.secondary {
    border-top: 1px solid var(--border);
    background: var(--surface-hover);
  }
  .kpi {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 10px 16px;
    border-right: 1px solid var(--border);
    min-width: 78px;
    white-space: nowrap;
  }
  .kpi:last-child { border-right: none; }
  .kpi-label {
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  .kpi-value {
    font-size: 16px;
    font-weight: 700;
    color: var(--text-bright);
  }
  .kpi.win .kpi-value { color: var(--win); }
  .kpi.loss .kpi-value { color: var(--loss); }
  .kpi.clickable { cursor: pointer; }
  .kpi.clickable:hover { background: var(--surface-hover); }
  .more-toggle {
    margin-left: auto;
    padding: 6px 12px;
    background: var(--surface-hover);
    border: none;
    border-left: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
  }
  .more-toggle:hover { color: var(--text); background: var(--border-strong); }
  .metrics-note {
    padding: 0 16px;
    font-size: 11px;
    color: var(--text-dim);
    white-space: nowrap;
  }
</style>
