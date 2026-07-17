<script>
  let { title, connected = false, error = '', children = null } = $props();
</script>

<header class="page-header">
  <h1 class="page-title">{title}</h1>
  <div class="status-bar">
    {@render children?.()}
    {#if connected}
      <span class="badge badge-ok">Connected</span>
    {:else}
      <span class="badge badge-err">Disconnected</span>
    {/if}
    {#if error}
      <span class="error-text">{error}</span>
    {/if}
  </div>
</header>
