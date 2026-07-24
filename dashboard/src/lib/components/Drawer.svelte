<script>
  let { open = false, title = '', onClose, children } = $props();

  function onKey(/** @type {KeyboardEvent} */ e) {
    if (e.key === 'Escape' && open) onClose?.();
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <div class="drawer-backdrop" role="button" tabindex="-1" onclick={() => onClose?.()} onkeydown={(e) => e.key === 'Enter' && onClose?.()}></div>
  <aside class="drawer">
    <header class="drawer-header">
      <h2 class="drawer-title">{title}</h2>
      <button class="drawer-close" onclick={() => onClose?.()} aria-label="Close">\u00D7</button>
    </header>
    <div class="drawer-body">
      {@render children?.()}
    </div>
  </aside>
{/if}

<style>
  .drawer-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 900;
    animation: fade-in 0.15s ease-out;
  }
  .drawer {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    width: 460px;
    max-width: 90vw;
    background: var(--surface);
    border-left: 1px solid var(--border-strong);
    z-index: 901;
    display: flex;
    flex-direction: column;
    animation: slide-in 0.2s ease-out;
    box-shadow: -4px 0 24px rgba(0, 0, 0, 0.4);
  }
  @keyframes fade-in { from { opacity: 0; } to { opacity: 1; } }
  @keyframes slide-in { from { transform: translateX(100%); } to { transform: translateX(0); } }
  .drawer-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .drawer-title {
    font-size: 16px;
    font-weight: 700;
    color: var(--text-bright);
    margin: 0;
  }
  .drawer-close {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 22px;
    cursor: pointer;
    padding: 0 4px;
    line-height: 1;
  }
  .drawer-close:hover { color: var(--text); }
  .drawer-body {
    flex: 1;
    overflow-y: auto;
    padding: 20px;
  }
</style>
