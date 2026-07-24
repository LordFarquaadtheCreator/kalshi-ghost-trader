<script>
  let { open = false, title = '', onClose, children } = $props();

  function onKey(/** @type {KeyboardEvent} */ e) {
    if (e.key === 'Escape' && open) onClose?.();
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <div class="modal-backdrop" role="button" tabindex="-1" onclick={() => onClose?.()} onkeydown={(e) => e.key === 'Enter' && onClose?.()}>
    <div class="modal" role="dialog" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <header class="modal-header">
        <h2 class="modal-title">{title}</h2>
        <button class="modal-close" onclick={() => onClose?.()} aria-label="Close">\u00D7</button>
      </header>
      <div class="modal-body">
        {@render children?.()}
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.6);
    z-index: 900;
    display: flex;
    align-items: center;
    justify-content: center;
    animation: fade-in 0.15s ease-out;
  }
  .modal {
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    width: 420px;
    max-width: 90vw;
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
    animation: modal-in 0.18s ease-out;
  }
  @keyframes fade-in { from { opacity: 0; } to { opacity: 1; } }
  @keyframes modal-in { from { opacity: 0; transform: scale(0.96); } to { opacity: 1; transform: scale(1); } }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
  }
  .modal-title {
    font-size: 16px;
    font-weight: 700;
    color: var(--text-bright);
    margin: 0;
  }
  .modal-close {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 22px;
    cursor: pointer;
    padding: 0 4px;
    line-height: 1;
  }
  .modal-close:hover { color: var(--text); }
  .modal-body {
    padding: 20px;
    overflow-y: auto;
  }
</style>
