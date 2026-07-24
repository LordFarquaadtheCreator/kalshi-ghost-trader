<script>
  import { toastStore, dismissToast } from '$lib/toast.js';
  let toasts = $derived($toastStore);
</script>

<div class="toaster">
  {#each toasts as t (t.id)}
    <div class="toast toast-{t.type}" role="button" tabindex="-1" onclick={() => dismissToast(t.id)} onkeydown={(e) => e.key === 'Enter' && dismissToast(t.id)}>
      <span class="toast-icon">
        {t.type === 'ok' ? '\u2713' : t.type === 'err' ? '\u2717' : '\u24D8'}
      </span>
      <span class="toast-text">{t.text}</span>
    </div>
  {/each}
</div>

<style>
  .toaster {
    position: fixed;
    bottom: 20px;
    right: 20px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 1000;
    pointer-events: none;
  }
  .toast {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 16px;
    border-radius: var(--radius-sm);
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    pointer-events: auto;
    min-width: 220px;
    max-width: 380px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
    animation: toast-in 0.18s ease-out;
  }
  @keyframes toast-in {
    from { opacity: 0; transform: translateX(20px); }
    to { opacity: 1; transform: translateX(0); }
  }
  .toast-ok { background: var(--win-bg); color: var(--win); border: 1px solid var(--win); }
  .toast-err { background: var(--loss-bg); color: var(--loss); border: 1px solid var(--loss); }
  .toast-info { background: var(--loading-bg); color: var(--loading); border: 1px solid var(--loading); }
  .toast-icon { font-size: 15px; flex-shrink: 0; }
  .toast-text { flex: 1; }
</style>
