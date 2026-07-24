<script>
  let { rows = 8, cols = 6 } = $props();
</script>

<div class="skeleton-wrap">
  <table class="data-table">
    <thead>
      <tr>
        {#each Array(cols) as _, i}
          <th><div class="skel skel-th"></div></th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each Array(rows) as _, r}
        <tr>
          {#each Array(cols) as _, c}
            <td><div class="skel skel-td" style="width: {50 + ((r + c) % 5) * 18}%"></div></td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .skeleton-wrap { overflow-x: auto; }
  .skel {
    background: linear-gradient(90deg, var(--surface-hover) 25%, var(--border-strong) 50%, var(--surface-hover) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: var(--radius-xs);
  }
  .skel-th { height: 12px; width: 60%; }
  .skel-td { height: 14px; }
  @keyframes shimmer {
    from { background-position: 200% 0; }
    to { background-position: -200% 0; }
  }
</style>
