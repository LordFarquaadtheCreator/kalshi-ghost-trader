<script>
  import { untrack } from 'svelte';
  let { title, count = null, defaultOpen = true, children } = $props();
  let open = $state(untrack(() => defaultOpen));
</script>

<div class="collapsible-section">
  <button class="section-header" onclick={() => open = !open}>
    <span class="collapse-icon">{open ? '▼' : '▶'}</span>
    <span class="section-title">{title}</span>
    {#if count !== null}<span class="section-count">— {count}</span>{/if}
  </button>
  {#if open}
    {@render children()}
  {/if}
</div>

<style>
  .collapsible-section { margin-bottom: 12px; }
  .section-header {
    display: flex; align-items: center; gap: 8px;
    background: none; border: none; cursor: pointer;
    padding: 0; width: 100%; text-align: left;
    font: inherit; color: var(--text-bright);
    font-size: 16px; font-weight: 600;
    margin: 20px 0 10px;
  }
  .section-header:hover { color: var(--text); }
  .collapse-icon { font-size: 10px; color: var(--text-muted); width: 14px; }
  .section-count { font-weight: 400; color: var(--text-muted); font-size: 14px; }
</style>
